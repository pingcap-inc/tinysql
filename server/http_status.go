// Copyright 2017 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/logutil"
	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
)

// Status server timeouts.
const (
	statusReadHeaderTimeout = 10 * time.Second
	statusReadTimeout       = 30 * time.Second
	statusWriteTimeout      = 60 * time.Second
	statusIdleTimeout       = 2 * time.Minute

	// maxPProfSeconds caps the duration a caller may request from any pprof
	// handler.
	maxPProfSeconds = 30
)

// limitPProfSeconds rejects a pprof "seconds" parameter that is not a
// positive number of at most maxPProfSeconds.
func limitPProfSeconds(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s := r.FormValue("seconds"); s != "" {
			sec, err := strconv.ParseFloat(s, 64)
			if err != nil || sec <= 0 || sec > maxPProfSeconds {
				http.Error(w, "seconds must be a positive number no greater than "+strconv.Itoa(maxPProfSeconds), http.StatusBadRequest)
				return
			}
		}
		h(w, r)
	}
}

// startStatusHTTP binds the status listener and serves it in the background.
// A bind error is returned to the caller instead of being logged and ignored.
func (s *Server) startStatusHTTP() error {
	addr := net.JoinHostPort(s.cfg.Status.StatusHost, strconv.Itoa(int(s.cfg.Status.StatusPort)))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Annotatef(err, "status server cannot listen on %s", addr)
	}
	logutil.BgLogger().Info("status server is running", zap.String("addr", addr), zap.Bool("pprof", s.cfg.Status.EnablePProf))
	go s.startHTTPServer(l)
	return nil
}

func (s *Server) startHTTPServer(l net.Listener) {
	router := mux.NewRouter()

	serverMux := http.NewServeMux()
	serverMux.Handle("/", router)

	if s.cfg.Status.EnablePProf {
		// Index also serves the named profiles (heap, goroutine, ...), which accept seconds too.
		serverMux.HandleFunc("/debug/pprof/", limitPProfSeconds(pprof.Index))
		serverMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		serverMux.HandleFunc("/debug/pprof/profile", limitPProfSeconds(pprof.Profile))
		serverMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		serverMux.HandleFunc("/debug/pprof/trace", limitPProfSeconds(pprof.Trace))
	}

	var (
		httpRouterPage bytes.Buffer
		pathTemplate   string
		err            error
	)

	err = router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		pathTemplate, err = route.GetPathTemplate()
		if err != nil {
			logutil.BgLogger().Error("get HTTP router path failed", zap.Error(err))
		}
		name := route.GetName()
		// If the name attribute is not set, GetName returns "".
		if name != "" {
			httpRouterPage.WriteString("<tr><td><a href='" + pathTemplate + "'>" + name + "</a><td></tr>")
		}
		return nil
	})
	if err != nil {
		logutil.BgLogger().Error("generate root failed", zap.Error(err))
	}
	if s.cfg.Status.EnablePProf {
		httpRouterPage.WriteString("<tr><td><a href='/debug/pprof/'>Debug</a><td></tr>")
	}
	httpRouterPage.WriteString("</table></body></html>")
	router.HandleFunc("/", func(responseWriter http.ResponseWriter, request *http.Request) {
		_, err = responseWriter.Write([]byte(httpRouterPage.String()))
		if err != nil {
			logutil.BgLogger().Error("write HTTP index page failed", zap.Error(err))
		}
	})

	s.setupStatusServer(l, serverMux)
}

func (s *Server) setupStatusServer(l net.Listener, serverMux *http.ServeMux) {
	m := cmux.New(l)
	m.SetReadTimeout(statusReadHeaderTimeout)
	// Match connections in order:
	// First HTTP, and otherwise grpc.
	httpL := m.Match(cmux.HTTP1Fast())

	s.statusServer = &http.Server{
		Addr:              l.Addr().String(),
		Handler:           CorsHandler{handler: serverMux, cfg: s.cfg},
		ReadHeaderTimeout: statusReadHeaderTimeout,
		ReadTimeout:       statusReadTimeout,
		WriteTimeout:      statusWriteTimeout,
		IdleTimeout:       statusIdleTimeout,
		MaxHeaderBytes:    1 << 16,
	}

	go util.WithRecovery(func() {
		err := s.statusServer.Serve(httpL)
		logutil.BgLogger().Error("http server error", zap.Error(err))
	}, nil)
	err := m.Serve()
	if err != nil {
		logutil.BgLogger().Error("start status/rpc server error", zap.Error(err))
	}
}

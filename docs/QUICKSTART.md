# Quick Start

TinySQL is built and run from source; there is no Docker image. See
[Run as MySQL protocol server](#run-as-mysql-protocol-server) below. The server
listens on `127.0.0.1` only by default (see the security notice in the
[README](../README.md#security-notice)).

#### __Or run TiDB on TiKV cluster__ 

Read the documents for [Ansible deployment](https://github.com/pingcap/docs/blob/master/op-guide/ansible-deployment.md) or [docker deployment](https://github.com/pingcap/docs/blob/master/op-guide/docker-deployment.md).

#### __Pre-requirement__

Go environment. Currently a 64-bit version of go >= 1.9 is required.
```
git clone https://github.com/pingcap/tidb.git $GOPATH/src/github.com/pingcap/tidb
cd $GOPATH/src/github.com/pingcap/tidb
make
```

#### __Run as MySQL protocol server__

```
make
cd bin && ./tidb-server
```

In case you want to compile a specific location:

```
make server TARGET=$GOPATH/bin/tidb-server
```

The default server port is `4000` and can be changed by flag `-P <port>`.

Run `./tidb-server -h` to see more flag options.

After you started tidb-server, you can use official `mysql` client to connect to TiDB.
Log in as `root` with the root password: the value of `TINYSQL_ROOT_PASSWORD` if
you set it, otherwise the password the server generated in the `root-password`
file of its data directory (default `tinysql-data/root-password`, relative to the
directory you started the server from).

```
mysql -h 127.0.0.1 -P 4000 -u root -p -D test --prompt="tidb> "
```

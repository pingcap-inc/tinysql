# TinySQL

TinySQL is a course designed to teach you how to implement a distributed relational database in Go. The name TinySQL indicates it is a simplifed version of [TiDB](https://github.com/pingcap/tidb).

## Prerequisites

Experience with Go is required. If not, it is recommended to learn [A Tour of Go](https://tour.golang.org/) first.

## Course Architecture

The entire course is designed based on [TiDB](https://github.com/pingcap/tidb).

The course is organized into six parts:

- [Simple explanation of SQL and relational algebra](./courses/proj1-README-zh_CN.md)
- [Parser](./courses/proj2-README-zh_CN.md)
- [DDL](./courses/proj3-README-zh_CN.md)
- [Optimizer](./courses/proj4-README-zh_CN.md)
- [Executor](./courses/proj5-README-zh_CN.md)
- [Percolator](./courses/proj6-README-zh_CN.md)

## Reference List

This [reference list](./courses/material.md) provides plenty of materials for you to study how a database system works. We pick some topics in it and prepare homework for you to have a better understand about it.

This course will take you from idea to implementation, with the essential topics of distributed relational database covered. 

The course is organized into three parts:

1. Gives a simple interpretation of SQL and relational algebra in preparation for the following course.

2. Explains the life of a read-only SQL, which includes parsing, execution, and the optimization of SQL plans.

3. Focuses on SQLs (including DML and DDL) that change the state of the database: how they get implemented and how to deal with the interaction of them and read-only statements.

## Other courses in this series

This course only focuses on the SQL layer of a distributed database system. If you are also interested in KV layer, see [TinyKV](https://github.com/pingcap-incubator/tinykv).

## Security notice

TinySQL is for learning. It has a single `root` account with full privileges and no privilege system, so anyone who has the root password can read and change all data.

- **Root password.** Set it with the `TINYSQL_ROOT_PASSWORD` environment variable. If you don't, the server generates one on first start and saves it in `root-password` inside the data directory (`-path`, default `/tmp/tinysql`), readable only by you. The server log shows the file's path, never the password. `TINYSQL_ROOT_PASSWORD` is required with `--store=tikv`.
- **TLS** is off unless you set `ssl-cert` and `ssl-key` in the `[security]` section of the config file (see `config/config.toml.example`).
- By default the SQL port (4000) and the status/debug port (10080) listen on `127.0.0.1` only.
- If you need to connect from another machine, or run TinySQL inside a Docker container, start it with `-host 0.0.0.0` (and `-status-host 0.0.0.0` for the status port), and only on a network you trust.
- Do not expose TinySQL to the internet or use it with real data.

## Deploy

Once you finish the project. You can deploy the binary and use MySQL client to connect the server.

### Build

```
make
```

### Run & Play

Use `./bin/tidb-server` to deploy the server and use `mysql -h127.0.0.1 -P4000 -uroot -p` to connect the server. Enter the root password from `TINYSQL_ROOT_PASSWORD` or from the `root-password` file in the data directory (see the [security notice](#security-notice)).

Also you can deploy the cluster together with the tinykv project.

Use `make` command in tinykv project to build binary `tinyscheduler-server`, `tinykv-server`.

Then put the binaries into a single dir and run the following commands:

```
mkdir -p data
./tinyscheduler-server
./tinykv-server -path=data
TINYSQL_ROOT_PASSWORD='<choose a password>' ./tidb-server --store=tikv --path="127.0.0.1:2379"
```
## Start to learn and join the learning class

Please register the talentplan course platform and join the [tinySQL learning class](https://talentplan.edu.pingcap.com/catalog/info/id:234)

## Autograding

Since Jun 2022, we start using [github classroom](./classroom.md) to accept labs and provide autograding timely. The github classroom invitation, the video content and the certification after you pass the class is  provided in the [tinySQL learning class](https://talentplan.edu.pingcap.com/catalog/info/id:234)

## Contributing

Contributions are welcomed and greatly appreciated. See [Contribution Guide](https://github.com/pingcap/community/tree/master/contributors) for details on submitting patches and the contribution workflow.

## License

TinySQL is under the Apache 2.0 license. See the [LICENSE](./LICENSE) file for details.

# Service Weaver Template

This repository contains a basic template of a Service Weaver application that
you can use via `gonew`. This template will allow you to get started using
Service Weaver more easily.

```shell
$ go install golang.org/x/tools/cmd/gonew@latest
$ gonew github.com/ServiceWeaver/template example.com/foo
```

## Run Weaver local-GKE

```shell
go install github.com/ServiceWeaver/weaver-gke/cmd/weaver-gke-local@latest
go install github.com/ServiceWeaver/weaver/cmd/weaver@latest
```

```shell
go build . -o my-app
weaver gke-local deploy weaver.toml
```

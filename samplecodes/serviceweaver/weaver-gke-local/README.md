
## Run Weaver local-GKE

### Initialize workspace

```shell
go mod init example.com/handson-serviceweaver-gke-local
go get github.com/ServiceWeaver/weaver@latest
```

### Install tools

```shell
go install github.com/ServiceWeaver/weaver/cmd/weaver@latest
go install github.com/ServiceWeaver/weaver-gke/cmd/weaver-gke-local@latest
```

```shell
go install github.com/ServiceWeaver/weaver-gke/cmd/weaver-gke-local@latest
go install github.com/ServiceWeaver/weaver/cmd/weaver@latest
```

```shell
go build . -o my-app
weaver gke-local deploy weaver.toml
```


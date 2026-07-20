# articles

A repository for articles and books.

## How to start development

### Install task

https://taskfile.dev/installation/

```
go install github.com/go-task/task/v3/cmd/task@latest
```

### Setup workspace

installing Zenn CLI and other dependencies.

[📘 About Zenn CLI](https://zenn.dev/zenn/articles/zenn-cli-guide)

```
task setup
```

### Preview workspace

```
task start
```

## Rules for AI Agents

### Always show a plan before `terraform apply` / `terraform destroy`

Before running `terraform apply` or `terraform destroy`, always show the user the `terraform plan` result (which resources will be added, changed, or destroyed) and get explicit confirmation. This matters most when creating or deleting billable resources. Use `-auto-approve` only after that confirmation.

### Cite sources for research results

When presenting results from documentation research or web searches, always include the source URL. If something is based on inference or guesswork, state explicitly that it is an inference.

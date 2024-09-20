# articles

A repository for articles and books.

## Emojis for Commit messages and PRs

Inspired by [gitmoji.dev](https://gitmoji.dev/) and  [GitCommitEmoji.md](https://gist.github.com/parmentf/035de27d6ed1dce0b36a)

| Commit or PR type                                      | Emoji             |
|--------------------------------------------------------|------------------|
| Add articles.                                          | :memo: `:memo:`    |
| Add books.                                             | :notebook_with_decorative_cover: `:notebook_with_decorative_cover:`  |
| Fix articles and books.                                | :pencil2: `:pencil2:`   |
| Publish articles or books.                             | :rocket: `:rocket:`    |
| Improve structure / format of the code.                | :art: `:art:`       |
| Remove code or files.                                  | :fire: `:fire:`      |
| Fix a bug.                                             | :bug: `:bug:`       |
| Refactor code.                                         | :recycle: `:recycle:`   |
| Introduce new features.                                | :sparkles: `:sparkles:`   |
| Add or update documentation.                           | :memo: `:memo:`      |
| Add, update, or pass tests.                            | :white_check_mark: `:white_check_mark:` |
| Add or update secrets.                                 | :closed_lock_with_key: `:closed_lock_with_key:` |
| Work in progress.                                      | :construction: `:construction:` |
| Add, update or fix CI build system.                    | :construction_worker: `:construction_worker:` |
| Changes in configuration files and scripts.            | :wrench: `:wrench:`    |
| Internationalization and localization.                 | :globe_with_meridians: `:globe_with_meridians:` |
| Move or rename resources (e.g.: files, paths, routes). | :truck: `:truck:` |
| Infrastructure related changes.                        | :bricks: `:bricks:` |
| Improve developer experience.                          | :technologist: `:technologist:` |

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

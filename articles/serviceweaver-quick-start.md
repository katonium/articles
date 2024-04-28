---
title: ""
emoji: "👏"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: []
published: false
---

## はじめに

Googleのエンジニアが開発した、ローカルでモノリシックに構築したアプリケーションをマイクロサービスとしてデプロイすることが可能なServiceWeaverというフレームワークがあります。
本記事ではServiceWeaverの特徴を動かしながらその特徴を実際に理解していきます。

https://serviceweaver.dev/

## ServiceWeaverの特徴

詳細は別記事で別途解説予定ですが、下記の特徴があります。
バージョン間の通信や複数のバイナリ管理といったマイクロサービスの運用で難しい部分を必要としない仕組みを導入することで、マイクロサービスの開発・運用をシンプルに実現することを狙ったフレームワークとなります。

1. 開発時には論理モジュール化されたモノリシックアプリケーションであること。
2. 実行時に論理モジュールに自動かつ動的に物理プロセスを割り当てること。
3. バージョンが異なるアプリケーション間で通信しない"アトミックなデプロイ"をすること。

:::message
記事執筆時点(2024/3/19)での最新バージョンは0.23.0であり、また安定版はありません。
今後破壊的な変更が入るリスクがあるため、アプリケーションに組み込む際には慎重な判断が必要であることをご理解ください。
:::

## インストール
Goが動く環境を前提として話を進めます。Goのインストールは[こちら](https://go.dev/doc/install)から。

Service Weaver CLIをインストールします。
```
go install github.com/ServiceWeaver/weaver/cmd/weaver@latest
```

Kubernetesにデプロイするためには別途`weaver-gke`もしくは`weaver-kube`のインストールが必要となります。
```
# 全自動でGKEにデプロイしたい場合
go install github.com/ServiceWeaver/weaver-gke/cmd/weaver-gke@latest
# それ以外の手法でKubernetesにデプロイしたい場合
go install github.com/ServiceWeaver/weaver-kube/cmd/weaver-kube@latest
```

このあと実際にコードを書いていくので、`hello/`ディレクトリを作成し初期化しておきます。

```bash
mkdir hello/
cd hello/
go mod init hello
```

## シンプルなアプリケーションの起動

ServiceWeaberの特徴として、マイクロサービスにおける各サービスの単位をコンポーネントと呼ばれる構造体によって定義します。
最もシンプルなServiceWeaberアプリケーションは下記のとおりです。`app`と呼ばれるコンポーネントのみの構成です。

```go:main.go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ServiceWeaver/weaver"
)

func main() {
    if err := weaver.Run(context.Background(), serve); err != nil {
        log.Fatal(err)
    }
}

// app - メインとなるコンポーネントの定義です。ServiceWeaverはこのコンポーネント単位に物理プロセスを割り当てます。
type app struct{
    weaver.Implements[weaver.Main]
}

// serveはweaver.Runによって呼び出されるメイン処理です。引数となるapp構造体はServiceWeaberによって注入されます。
func serve(context.Context, *app) error {
    fmt.Println("Hello")
    return nil
}
```

上記の`main.go`を作成した`hello/`ディレクトリ配下に配置し、下記のコマンドにてアプリケーションを起動すると、"Hello"と表示されます。
```bash
go mod tidy
weaver generate .
go run .
# -> Hello
```

## 複数のコンポーネントからなるアプリケーションの起動

次に、複数のコンポーネントからなるアプリケーションを起動します。`reverser.go`を作成し`Reverser`コンポーネントを定義します。

```go:reverser.go
package main

import (
    "context"

    "github.com/ServiceWeaver/weaver"
)

// Reverser - Reverserコンポーネントとしてのインターフェースを示す。
type Reverser interface {
    Reverse(context.Context, string) (string, error)
}

// reverser - Reverser componentのinterfaceを満たす実装。起動時に注入される。
type reverser struct{
    weaver.Implements[Reverser]
}

// Reverse - 与えられた文字を反対にして返す
func (r *reverser) Reverse(_ context.Context, s string) (string, error) {
    runes := []rune(s)
    n := len(runes)
    for i := 0; i < n/2; i++ {
        runes[i], runes[n-i-1] = runes[n-i-1], runes[i]
    }
    return string(runes), nil
}

```

`main.go`も下記の通り修正し、`Reverser`を呼び出すようにします。

```go:main.go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ServiceWeaver/weaver"
)

func main() {
    if err := weaver.Run(context.Background(), serve); err != nil {
        log.Fatal(err)
    }
}

type app struct{
    weaver.Implements[weaver.Main]
    reverser weaver.Ref[Reverser] // Reverserへの参照を定義
}

// serve - ServiceWeaberが起動する処理。app, Reverserは起動時に注入される。
func serve(ctx context.Context, app *app) error {
    // Reverseコンポーネントを呼び出して文字列の順序を逆転させる
    var r Reverser = app.reverser.Get()
    reversed, err := r.Reverse(ctx, "!dlroW ,olleH")
    if err != nil {
        return err
    }
    fmt.Println(reversed)
    return nil
}
```

- API定義がインターフェース定義に、API呼び出しが関数呼び出しになっている。インターフェース定義用のYamlを作る等と比較し、非常にシンプルな実装となることがわかる。


とりあえず、コードを見てもらうのが良いかと思うので完成品のコードを示します。
作成するアプリケーションはService Weaverの[Step by Step Tutorial](https://serviceweaver.dev/docs.html#step-by-step-tutorial)と同じく下記の構成をとります。ここまでに至るまでは本家の[Step by Step Tutorial](https://serviceweaver.dev/docs.html#step-by-step-tutorial)で少しずつアプリケーションを構築しながら解説していますのでそちらをご参照ください。



このコンポーネントと呼ばれる単位がマイクロサービスアーキテクチャにおける個々のサービス単位となります。






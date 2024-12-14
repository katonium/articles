---
title: "マイクロサービスの課題とうまく付き合っていく【論文紹介】"
emoji: "👏"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["ServiceWeaver", "go", "microservice", "マイクロサービス"]
published: true
---

:::message
この記事は[GCP(Google Cloud Platform) Advent Calendar 2024](https://qiita.com/advent-calendar/2024/gcp) 15日目の記事です。

また、[Jagu'e'r](https://jaguer.jp/) (Google Cloud User Community) の[クラウドネイティブ分科会Meetup ~学びの秋・食欲の秋・読書の秋 ビアバッシュLT ~](https://jaguer-cloud-native.connpass.com/event/330076/)にて2024/11/29に発表した内容をもとにした記事です。
:::


## はじめに

Googleのエンジニアが2023年6月に発表した[Towards Modern Development of Cloud Applications](https://dl.acm.org/doi/10.1145/3593856.3595909)という論文しつつ、マイクロサービスアーキテクチャの難しい部分とどう付き合っていくかを考える記事です。（PDFへの直リンクは[こちら](https://dl.acm.org/doi/pdf/10.1145/3593856.3595909)）

マイクロサービスの課題を分析しそれを解決するためのコンセプトに関して提案した記事であり、またプロトタイプ実装として[ServiceWeaver](https://serviceweaver.dev/)というプロダクトを開発しています。

https://dl.acm.org/doi/10.1145/3593856.3595909

本記事では論文の内容をまとめることで理解を深めながら、さらにはこのコンセプトが解決できなかった課題とどう立ち向かうべきなのか？について考察することを目的としています。

ServiceWeaverについて理解することで論文の内容が分かりやすくなるので、ServiceWeaverについても軽く触れながら解説していきます。

:::message
ServiceWeaverは2024年12月5日にメンテナンスモードに移行しており、2025年6月6日にサポートが終了する予定です。
ServiceWeaverをアプリケーションに導入するためには既存のアプリケーションの大部分を書きなおす必要があること等から導入のハードルが高いことが原因として挙げられています。

https://github.com/ServiceWeaver/weaver/blob/main/README.md

本記事はServiceWeaverを含んだ提案されているコンセプトを理解しマイクロサービスアーキテクチャのより良い構築方法を理解するためのものであり、ServiceWeaverの導入を推奨するものではありません。
:::

## マイクロサービスアーキテクチャの課題とその原因

### マイクロサービスのメリット
さまざまな調査により、ほとんどの開発者が次のいずれかの理由でアプリケーションを複数のバイナリに分割している（マイクロサービスを採用している）ことがわかりました。
1. **パフォーマンス**：個別のサービスを個別にスケーリングできるため、必要な分だけリソースを使用することができます。
2. **耐障害性**：1つのマイクロサービスがクラッシュしても他のマイクロサービスはダウンしないため、バグや障害の影響範囲を制限することができます。
3. **アプリケーション境界の明確化**：APIによってマイクロサービス間を明確に区切ることで、サービス内のコードの複雑化を防ぎます。
4. **柔軟なデプロイ**：異なるバイナリを異なるレートでリリースできるため、より機敏なコードのアップグレードが可能になります。

文中では紹介されていませんでしたが、技術の多様性やチーム間の独立性といったメリットもあげられることができます。

### マイクロサービスのデメリット
ただし、マイクロサービスアーキテクチャにはデメリットが存在し、これらのデメリットによってメリットが打ち消されてしまう場合もあると分析しています。

1. **パフォーマンス懸念**：データのシリアライズとネットワーク通信はパフォーマンスを下げる。
2. **正確性の懸念**：バージョン間の通信の正確性を担保することが難しい。
3. **サービス全体の管理が大変**：N個のバイナリを管理することが難しい。E2Eテストの難易度が高くなっていく。ローカル環境でアプリケーションを1つ動かしたいだけなのに、複数のリポジトリからコードをプルして動かす必要があったりとローカル環境での開発も大変となる。
4. **APIの変更難易度をあげる**：APIの変更が難しく、互換性を保つため昔のAPIを残しつつ新しいAPIを作成し、すべてのサービスのバージョンアップが終わったら古いAPIを削除するようなデプロイ方法が必要になることも。
5. **アプリケーションの開発を遅くする**：複数のサービスに影響がある変更を加えようとした際に、どうやってN個のマイクロサービスに変更を反映させデプロイするか検討する必要がある。

パフォーマンスはマイクロサービスのメリットとしても挙げられていた部分ですが、デメリットとしても挙げられてしまっています。

### マイクロサービスのデメリットの本質はなにか

これらは基本的に、マイクロサービスが論理境界 (コードの記述方法) と物理境界 (コードのデプロイ方法) を混同しているためと筆者は考察しています。

私の理解ですが、**開発時にコード上で分割したい関心事の違い（例えば認証認可といった機能の単位、顧客管理・商品管理といったチームの単位）** に応じたアプリケーションの境界は、必ずしも**実行時の境界（マイクロサービス間の境界）とは一致せず、密に通信するサービス感が地理的に離れたところに配置される** ことによってレイテンシが増加する、といったことが挙げられるのかなと思います。

![confusion-of-microservice-boundaries.png](/images/confusion-of-microservice-boundaries.png)


### マイクロサービスをより良く使うための方法

これらの課題に対する過去の取り組みとして、CICDやgRPC等があげられるものの、デメリットとして挙げられていた5つの要素すべてを解消するプロダクトはありません。

筆者らはこの課題5つ全てを解決するコンセプトを提唱します。筆者らの手法は以下3つのコンセプトによって構成されます。
1. **開発時には論理モジュール化されたモノリシックアプリケーションであること。**
2. **実行時に論理モジュールに自動かつ動的に物理プロセスを割り当てること。**
3. **バージョンが異なるアプリケーション間で通信しない"アトミックなデプロイ"をすること。**

これらのコンセプトがマイクロサービス開発に盛り込まれることで、マイクロサービスのメリットを最大限に活かしつつ、デメリットを最小限に抑えることができるとしています。

![solution-of-microservice-boundaries.png](/images/solution-of-microservice-boundaries.png)

## コンセプト実装「ServiceWeaver」

提案コンセプト自体は非常に複雑ですが、筆者らはプロトタイプ実装であるServiceWeaverというGo製フレームワークを開発しています。
残念ながら現在メンテナンスモードに移行していますが、Service Weaverの[Step by Step Tutorial](https://serviceweaver.dev/docs.html#step-by-step-tutorial)を見ることで、ServiceWeaverの下記のコンセプトを理解することができます。

- 開発環境ではシングルバイナリでアプリケーションをモノリシックに開発し、デプロイ時にはマイクロサービスアプリバージョンとしてデプロイすることが出来る
- サービス間の通信はアプリケーション開発者ではなくフレームワーク側が自動でコントロールする

また、インターフェースがGoのinterfaceで記載することができ、OpenAPIやProtoといったインターフェース定義を書く必要がないことは、開発者にとっても非常に使いやすいと感じました。（外部に公開するAPIについてはOpenAPI定義は必要になる気はしつつ、内部にのみ公開されるAPIはシンプルに実装できそう）

https://serviceweaver.dev/

:::details 付録：実際にServiceWeaverアプリケーションを実装してみる

本セクションでは[Step by Step Tutorial](https://serviceweaver.dev/docs.html#step-by-step-tutorial)の実装を紹介しつつ、ServiceWeaverのコンセプトを理解していきます。

### ServiceWeaverインストール
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

### シンプルなアプリケーションの起動

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

### 複数のコンポーネントからなるアプリケーションの起動

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

アプリケーションを実行

```bash
go mod tidy
weaver generate .
go run .
# -> Hello, World!
```

ローカル開発のためモノリシックアプリケーションとして起動していますが、本番環境にデプロイする際には各インターフェースごとにマイクロサービスとしてデプロイされます。

:::

## マイクロサービスのデメリットはなくならない

論文筆者の主張に反しますが、ServiceWeaverといったフレームワークを実際に導入したとしても、マイクロサービスの課題すべてを解決するわけではないと個人的には感じています。

例えば、下記のような課題が依然として残ると考えられます。

- DBやメッセージング等外部サービスを利用するシステムにおける破壊的変更時のデプロイの大変さ
- インターフェース変更時のチーム間でのコミュニケーション
- ランタイムのブラックボックス化による障害分析難易度の上昇
- エラー発生時にアプリケーションを止めないための仕組み・テスト

マイクロサービスアーキテクチャで構成されるシステムにはワークロードだけでなく、データベースやメッセージブローカー、そして開発者や営業等のチームも関わっています。
すべてのアーキテクチャ選択は利点と欠点のトレードオフであり、これらの構成要素が持つ課題すべてを一度に解決できる銀の弾丸はないと考えています。

## まとめ：マイクロサービスのデメリットを理解しつつ付き合っていく

マイクロサービスのデメリットを **「なくす」** ための銀の弾丸を探すのではなく、 **「理解しながら付き合っていく」** ことが重要なのではないでしょうか。 

マイクロサービスのデメリットは許容しつつも物理的な境界を意識したサービスの分割を心がけ、不必要なマイクロサービス乱立を避ける設計が良さそうです。

物理的な境界を意識しサービス単位を設計する
- 物理的に結合度が高いサービスは同じサービスにまとめられないか検討する

不必要なマイクロサービス化を避け、適度に機能を集約させる
- サービスが乱立するとメンテナンスコストが上がることを念頭に置く
- 開発の初期はモジュラーモノリスで開発しつつ、システムが大きくなったら分割※
- メインの機能はモジュラーモノリスで構築し、周辺のシステム拡張をマイクロサービス的に実施する

※Slackはかつてモノリシックアーキテクチャを採用していました。現在はセルラーアーキテクチャと呼ばれるいくつかのレプリカを持つアーキテクチャに移行しているようです。

https://slack.engineering/slacks-migration-to-a-cellular-architecture/

※Shopigyも以前はモノリシックアーキテクチャを採用していましたが、ビジネスの拡大に伴ってモジュールの分割を実施したようです。

https://mehmetozkaya.medium.com/shopifys-modular-monolithic-architecture-a-deep-dive-%EF%B8%8F-a2f88c172797


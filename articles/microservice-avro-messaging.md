---
title: "マイクロサービス間のメッセージングスキーマをapache Avroとgogen-avroで効率的に管理する"
emoji: "🗂"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["go", "googlecloud", "pubsub"]
published: false
notes:
    - replace-check:
        - トランスパイル: コンパイル
        - Golang: Go言語
        - Apache avro: apache Avro？
    - other: 英字に必要に応じて``を付ける
    - other: ですます調の確認
others:
    - Avroとその他類似の手法の違いについて（ProtoやSwaggerを用いたHTTP/gRPC APIのスキーマ管理）→Avroはシリアライズによってメッセージのサイズを削減できるためメッセージングに特化している？？
    - 可能ならインフラも同様に管理したいが、Terraformでやる手法はよく分からなかった。submodulesを使用してスキーマファイルを参照させれば出来そうだったが、Go言語のように一元管理する方法があればうまく使いたいが、どうなのか。
    - 書きたい内容が混ざっているかも
        - マイクロサービスにおいて、avroをgoに変換することでgo.modに一元化出来て便利
        - submoduleを使っても出来る→別記事
        - terraform moduleを使えばタグ指定できるかも。そこまで出来たらさらに便利→別記事
            - Terraform moduleを使う場合同じリポジトリの複数のスキーマを指定できるのかも大事になってきそう→メジャー違いのQueueが共存する場合など
---


## はじめに
* マイクロサービス間の通信スキーマを管理することで開発速度の向上・運用の安定化に寄与することが出来る。
* 非同期通信のスキーマを管理するために、AvroとGitとGoを使ってgo.modにてバージョンを管理する手法を紹介する。

### マイクロサービス間の通信スキーマ管理の重要性

* 通信スキーマ管理の重要性
    * 特に非同期通信においてはスキーマの誤りが発生すると結構大変
        * Subscriber側で見つかることが多いが、その時点からのリカバリが結構厳しい（オンライン処理のように呼び出し側に失敗を通知することが難しい）
    * スキーマはバージョン
    * また、アプリケーションコードと変更のサイクルが異なるため、リポジトリを分けることが望ましい
* 複数リポジトリ間でのバージョン管理の難しさ
    * Goやその依存モジュールのバージョン管理に加えAvroスキーマの管理が発生すると見通しが悪くなる

- どれくらいの読者を想定するかによるが、マイクロサービスでイベントハブを使った通信がある（サービス間の疎結合化）。ただし送受信のスキーマに差異があると大変なのでスキーマ管理が重要となる。
- スキーマのずれに関して: 後方互換があるのかどうかは重要。MAINRIの場合にはセマンティックバージョニングを用いて、互換性の有無を管理している。

### Apache Avroとは
* Apache avroの紹介→どこまでちゃんと書くか？

- 軽くで良さそう

### MAINRIでは
- ソリューションの紹介


## 実装例の紹介
### 想定するマイクロサービス間の通信
* パブリッシャーとサブスクライバがPub/Subを介してつながっている状態

- 上と重複しているかも

### リポジトリ構成
* 今回は4つのリポジトリを使用した構成とする

Avro Schema -> (CI/CD)※別記事の範囲かも。ここではShellscript使う -> Avro Golang repo
Publisher(Go) -> Messaging Service (Cloud Pub/Sub) -> Subscriber(Go)

* 実際に作成したので、手元で実行するときなどに使ってほしい

### 環境の準備
* 解説しない
    * Google Cloudアカウント開設
    * Goのインストール
* 引用で終わらせる
    * トピック・サブスクリプションの作成→クイックスタートのドキュメントを参照させる
* 解説する
    * gogen-avroのインストール

また、AvroスキーマをGo言語にコンパイルするために今回は[gogen-avro](https://github.com/actgardner/gogen-avro/)を使用します。`go install`を使って取得しておきます。
```
go install github.com/actgardner/gogen-avro/v10/cmd/...@v10.2.1
```

### スキーマの作成
* Avroスキーマを作成する
```json:sample.avsc
# FIXME
```

### スキーマのコンパイル
* AvroスキーマをGoにコンパイルする
* 実際にはCICDしたいけど、大変なのでShellscriptで

```bash:compile.sh
# FIXME
```

### アプリケーションの実装
* アプリケーションにコンパイルしたスキーマモジュールを追加する
* アプリケーションを実装する。シンプルな実装にする。
* 実装したアプリケーションを疎通させてみる

```go:subscriber.go
package main

```
:::details publisher.go
```go:publisher.go
package main

// publish - トピックにメッセージをパブリッシュする
func publish() error {
	ctx := context.Background()

	// Pub/Subクライアントを作成
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return fmt.Errorf("pubsub: NewClient: %w", err)
	}
	defer client.Close()

	// パブリッシュするメッセージを定義
	msg := []byte(`{"StringField":"hello","FloatField":3.14,"BooleanField":true}`)

	// メッセージをパブリッシュする
	t := client.Topic(topicID)
	result := t.Publish(ctx, &pubsub.Message{
		Data: msg,
	})

	// Get()はパブリッシュが完了するまで待機し、Pub/Subにより発行されるメッセージIDを返す
	id, err := result.Get(ctx)
	if err != nil {
		return fmt.Errorf("pubsub: result.Get: %w", err)
	}
	fmt.Printf("message published.  ID:%s size:%d\n  message:%s\n", id, len(msg), msg)
	return nil
}
```
:::

* publisher実行

```bash
go run ...
```

* subscriber実行

```bash
go run ...
```

* publisherで送信したメッセージを受信できていることが分かる
* 同じスキーマを参照し疎通することが出来た

- スキーマのバージョンの違いでどうなるか（互換性があるとき、ないとき）

## まとめ
* マイクロサービスにおいて、avroスキーマのバージョンを効率的に管理する方法を紹介した。

### 余談1: 今回試さなかったこと

* 記事が長くなってしまうので省略しましたが、実際にマイクロサービスを構築するときに実装することになりそうなものを下記に記載します。

#### Cloud Pub/Subトピックへのスキーマ設定

* Cloud Pub/SubにおいてはトピックにAvroスキーマを設定することができる。

#### TerraformでのCloud Pub/Subスキーマ管理
* Cloud Pub/Subへスキーマを設定する場合、作成したAvroスキーマをうまく使用しTerraformにてスキーマのバージョン管理を行えるとなお良いと感じるが、実際に出来るのかは未調査である。

### 余談2: 他の選択肢に関する考察

今回はAvroをGoにコンパイルする方法を紹介したが、より簡素に実装するためには下記の方法が考えられる。今回紹介した方法と比較したメリット・デメリットに関しても考える。

#### 1つのリポジトリに全てのコードを統合する
Avro・送信側アプリケーション・受信側アプリケーションを1つのリポジトリで管理し、コミット断面での動作を保証する。
非常に小さなアプリケーションや、開発の初期段階ではこの手法でも良いと考えられるが、管理が煩雑になる点やスキーマ・送/受信アプリケーション開発者が別であることが多いと思うので、この場合には逆に開発効率が落ちることが考えられる。
（筆者も個人開発の初期段階では1つのリポジトリにすべてのアプリケーションを書くことが多いが、体感としても開発が進むとリポジトリを分散させた方が楽に感じる）

#### Git submoduleを用いたバージョン管理
リポジトリを分けて管理することが可能。ただしgo.modとsubmoduleによるアプリケーションの依存性管理が共存するため分かりづらくなると感じるので推奨しない。
余談だがアプリケーションが社内の共有ライブラリに依存する場合も同様にsubmoduleよりgo.modに一元管理させた方が見通しが良くなる印象。

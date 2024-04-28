---
title: "Go製DBスキーマ管理ツールのAtlasを触ってみた"
emoji: "🛸"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["atlas", "go", "mysql", "tech"]
published: true
---

## はじめに
個人で開発をする際にDBのバージョン管理・マイグレーションに結構苦労したのもありGo製DBマイグレーションツールのAtlasを導入してみました。
使い勝手が良いツールで仕事でも使いたいなと思ったので、Quick Startの内容を追いかけながら特徴をまとめていきます。

https://atlasgo.io/

https://github.com/ariga/atlas


## Atlasとは

Databaseのスキーマを管理・変更・可視化するためのツールです。

HCLもしくはSQLを使用し理想状態のスキーマを作成することで、Atlasが現在との差分を比較し変更してくれます。
対応するデータベースの種類も多く、またCLIでの利用・CICDでの利用の両方が想定されているためローカル開発と本番運用の両方に適用することが出来ます。

また、Atlas Cloudというクラウドサービスを利用することで作成したスキーマを可視化することが出来ます。
ログインしなくても他の人が作成したスキーマを見ることが出来るので、ちょっと見てみると雰囲気が分かると思います。

![Atlas Cloudによるスキーマの可視化](/images/atlas-rakuraku-migration-visualization.png)

https://gh.atlasgo.cloud/explore

## なぜAtlasを選んだのか

上述した可視化が出来るところも良いなと感じたのですが、採用を決めたのは主に2つの理由からです。

### 1. マイグレーションファイルの管理がいらない

Atlasはスキーマの理想状態を宣言的に管理することが出来、バージョン間の差分を積み重ねるマイグレーションの方式を取りません。
そのため、マイグレーションファイル（バージョン間の差分のDDL）の管理を不要とし、管理が必要なのは理想状態のスキーマのみとなっています。

またマイグレーションファイルを用いたバージョン管理にも対応しているので、マイグレーションファイルを用いてバージョンという形で状態を管理することもできます。

このあたりはTerraformの思想に非常に近いと感じており、ライフサイクル的にはDBのスキーマはアプリケーションほど頻繁に更新されない特性からインフラリソースに近いという印象を持っていたためTerraform同様宣言的に管理することには納得感があります。

### 2. 特定の言語のランタイムに依存しない

PythonやJava等で作られたマイグレーションツールも見かけたのですが、実行のためにその言語のランタイムを必要とする場合があり、言語のバージョンとツールのバージョンを管理することに抵抗がありました。
AtlasはGo製のツールではあるものの、実行にGoを必要としません。JavaやPython等他の言語への依存を増やしたくなかったことからAtlasを選択しました。
またGoにも依存しないという特徴から、Go以外の言語でアプリを書いている人にとっても使い勝手が良いツールであると感じています。

## クイックスタート

ここからはAtlas公式サイトのQuick Introductionを進めていきます。

https://atlasgo.io/getting-started/

:::message
Dockerが使える環境であることを前提として解説します。Docker使える環境がない場合はインストールするか、[GitHub Codespaces](https://zenn.dev/yuhei_fujita/articles/github-codespaces-introduction)等を利用するのもおすすめです。（ブラウザでも動きます&デフォルトでDockerは使えるようになっています）
:::

### 1. Atlasのインストール

[サイト記載のとおり](https://atlasgo.io/getting-started/#installation)Atlas CLIをインストールします。インストール方法はOSによって異なり、下記はLinuxでの例です。

```bash
curl -sSf https://atlasgo.sh | sh
```

`Installation successful!`と出たらインストール成功です。`atlas version`を使って正常にインストールできているか確認してください。

```bash
atlas version
```
正常にインストールされている場合、下記のような表示が出ます。

```bash
atlas version v0.20.1-a4257be-canary
https://github.com/ariga/atlas/releases/latest
```

:::message
Windowsだと`.exe`ファイルをダウンロードしたあと`PATH`に置くように言われます。筆者の場合は`atlas-windows-amd64-latest.exe`というファイル名でダウンロードされたので、PATHに配置した後に`atlas.exe`に名前の変更が必要でした。

※PATHってなんだみたいな方はとりあえず`C:\Users\{ログインしているユーザー名}\bin\atlas.exe`となるようにダウンロードした`atlas-windows-amd64-latest.exe`をリネームし配置してください。
:::

### 2. テスト用のMySQLコンテナを起動し、テーブルを作成する

まずは`atlas-demo`コンテナを起動します。
```bash
docker run --rm -d --name atlas-demo -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=pass -e MYSQL_DATABASE=example mysql:latest
```

次に、下記のSQLで定義される`users`テーブルを作成します。

```bash
docker exec atlas-demo mysql -ppass \
  -e 'CREATE table example.users(id int PRIMARY KEY, name varchar(100))'
```

上記`docker exec`コマンドは`docker run`の実行後すぐ打つとエラーになるので、`docker logs`で立ち上がったコンテナのログを見つつMySQLの初期化を少し待ってから実行してください。

```bash
docker logs atlas-demo
```

```sql
CREATE table users (
  id int PRIMARY KEY,
  name varchar(100)
);
```

正常に作成された場合、わりとあっさりしたメッセージが出るので心配な人は何度も打ってもOKです。
既に正常に実行されていると、下記のようにテーブルが存在するエラーが出ます。

```bash
mysql: [Warning] Using a password on the command line interface can be insecure.
ERROR 1050 (42S01) at line 1: Table 'users' already exists
```

### 3. スキーマの取込み

先ほどMySQLにて作成した`users`テーブルの内容をAtlasに取り込んでいきます。

```
atlas schema inspect -u "mysql://root:pass@localhost:3306/example" > schema_users.hcl
```

すると、取り込んだ結果の`schema_users.hcl`が作成されます。

```hcl:schema_users.hcl
table "users" {
  schema = schema.example
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = true
    type = varchar(100)
  }
  primary_key {
    columns = [column.id]
  }
}
schema "example" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
```

:::message
Getting Startedでは`schema.hcl`を作成しますが、解説の都合上`schema_users.hcl`とします。
:::

SQLで取り出すことも可能です。記事が長くなるので、興味がある方は↓をご参照ください。

:::details SQLで実行する場合の方法はこちら

下記のコマンドを実行します。

```
atlas schema inspect -u "mysql://root:pass@localhost:3306/example" --format '{{ sql . }}' > schema.sql
```

するとSQLファイルが生成されます

```sql:schema.sql
-- Create "users" table
CREATE TABLE `users` (`id` int NOT NULL, `name` varchar(100) NULL, PRIMARY KEY (`id`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

HCLファイルでインポートした際にはスキーマとして定義されていた文字コードの情報がSQLでインポートした場合にはテーブルに紐づく情報として取り出されているのはちょっと面白いなと感じます。MySQLのdumpファイル等を見る限り通常のSQLでスキーマレベルの文字コード定義は表現できないようで、それが原因なのかなと思います。

:::

### 4. スキーマの変更

次にHCLファイル上に新しいテーブル`blog_posts`の定義をを追加します。tableブロックを1つ追加することになりますが、どのスキーマを使っているのかわかりやすいという解説の都合上`schema_users.hcl`をコピーし`schema_users_and_blogposts.hcl`という別ファイルを作成し、そちらに追記します。

```diff hcl:schema_users_and_blogposts.hcl
table "users" {
  schema = schema.example
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = true
    type = varchar(100)
  }
  primary_key {
    columns = [column.id]
  }
}
+table "blog_posts" {
+  schema = schema.example
+  column "id" {
+    null = false
+    type = int
+  }
+  column "title" {
+    null = true
+    type = varchar(100)
+  }
+  column "body" {
+    null = true
+    type = text
+  }
+  column "author_id" {
+    null = true
+    type = int
+  }
+  primary_key {
+    columns = [column.id]
+  }
+  foreign_key "author_fk" {
+    columns     = [column.author_id]
+    ref_columns = [table.users.column.id]
+  }
+}
schema "example" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
```

作成した`schema_users_and_blogposts.hcl`をデータベースに適用していきます。`atlas schema apply`コマンドを打つことで、AtlasがDBの状態と現在のスキーマファイルの内容を見比べて差分を埋めるためのSQLを作成してくれます。

```bash
atlas schema apply -u "mysql://root:pass@localhost:3306/example" --to file://schema_users_and_blogposts.hcl
```

実行結果を見ると`blog_posts`を作成するためのSQLが表示されていることが分かります。FOREIGN KEYも適切に設定されています。
変更を適用するか聞かれますが、今回はApplyを選択し、マイグレーションを実行します。

```bash:実行結果
-- Planned Changes:
-- Create "blog_posts" table
CREATE TABLE `blog_posts` (
  `id` int NOT NULL,
  `title` varchar(100) NULL,
  `body` text NULL,
  `author_id` int NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `author_fk` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`)
);
? Are you sure?: 
  ▸ Apply
    Lint and edit (requires login)
    Abort
```


HCLではなくSQLを使っている場合はCREATE TABLE文を1つ追加するのみです。下記にSQLを使ってスキーマを作成している場合の例も示しておきます。

:::details SQLでスキーマを定義している場合はこちら

```diff sql:schema_users_and_blogposts.sql
 -- Create "users" table
CREATE TABLE `users` (`id` int NOT NULL, `name` varchar(100) NULL, PRIMARY KEY (`id`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
+
+-- create "blog_posts" table
+CREATE TABLE `blog_posts` (
+  `id` int NOT NULL,
+  `title` varchar(100) NULL,
+  `body` text NULL,
+  `author_id` int NULL,
+  PRIMARY KEY (`id`),
+  CONSTRAINT `author_fk` FOREIGN KEY (`author_id`) REFERENCES `example`.`users` (`id`)
+);
```

HCLファイルの場合と同様なコマンドで実行します。

```bash
atlas schema apply   -u "mysql://root:pass@localhost:3306/example"   --to file://schema_users_and_blogposts.sql   --dev-url "docker://mysql/8/example"
```

実行結果も同様なものとなります。

```bash:実行結果
-- Planned Changes:
-- Create "blog_posts" table
CREATE TABLE `blog_posts` (
  `id` int NOT NULL,
  `title` varchar(100) NULL,
  `body` text NULL,
  `author_id` int NULL,
  PRIMARY KEY (`id`),
  INDEX `author_fk` (`author_id`),
  CONSTRAINT `author_fk` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
? Are you sure?: 
  ▸ Apply
    Lint and edit (requires login)
    Abort
```

:::

ちなみに差分がないとこんなメッセージが出てコマンドが終了します。
```
Schema is synced, no changes to be made
```

### 4. スキーマの可視化

作成したスキーマをAtlas Cloudにアップロードして可視化します。

```bash
atlas schema inspect \
   -u "mysql://root:pass@localhost:3306/example" \
   --web
```

```bash:実行結果
? Where would you like to share your schema visualization?: 
  ▸ Publicly (gh.atlasgo.cloud)
    Your personal workspace (requires 'atlas login')
```

非公開な形でパーソナルワークスペースにアップロードするためには、事前にサインアップした上で`atlas login`コマンドを打ってログインしておく必要があります。今回は機密情報など含まれていないため`Publicly`を選択し、パブリックに公開します。
公開したスキーマ：[https://gh.atlasgo.cloud/explore/b2e21987](https://gh.atlasgo.cloud/explore/b2e21987)

![作成したスキーマの可視化結果](/images/atlas-rakuraku-migration-my-visualization.png)

サインインするとprivateな形で公開も可能ですが、特に機能などに違いはなさそうでした。

### 5. バージョンを管理しながらマイグレーション

Atlasはマイグレーションファイルのバージョン管理を必要としませんが、migration用のSQLを作成しマイグレーションファイルのバージョン管理運用も可能です。

チュートリアルでやった流れの通り、usersテーブルのみのマイグレーションを作成してみます。

```
atlas migrate diff create_blog_posts --dir "file://migrations" --to "file://schema_users.hcl" --dev-url "docker://mysql/8/example"
```

`migrations`配下に`users`テーブルのみを作成するためのSQLが作成されます。`atlas migrate diff [出力ファイル名]`となっているようで、`20240324052603_create_blog_posts.sql`というファイル名でマイグレーション用のSQLファイルが作成されました。

また、`atlas.sum`というファイルも作成されました。このファイルはマイグレーションファイルを作成するために更新されるため、複数のブランチでマイグレーションを作成してしまった場合にもGitのマージのタイミングで検知できるようになっているとのことです。

:::details 作成されたSQL

```sql:migrations/20240324052603_create_blog_posts.sql
-- Create "users" table
CREATE TABLE `users` (`id` int NOT NULL, `name` varchar(100) NULL, PRIMARY KEY (`id`)) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

:::

次に、blogpostsを含むスキーマからマイグレーションを作成します。
`users`テーブルは既に定義されているため、差分となる`blogposts`を作成するためのSQLのみが作成されることが分かります。

```
atlas migrate diff create_blog_posts --dir "file://migrations" --to "file://schema_users_and_blogposts.hcl"   --dev-url "docker://mysql/8/example"
```

:::details 作成されたSQL

```sql:migrations/20240324052626_create_blog_posts.sql
-- Create "blog_posts" table
CREATE TABLE `blog_posts` (`id` int NOT NULL, `title` varchar(100) NULL, `body` text NULL, `author_id` int NULL, PRIMARY KEY (`id`), INDEX `author_fk` (`author_id`), CONSTRAINT `author_fk` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

:::


## その他試したこと

Getting Startedの内容は以上で終わりですが、他にもいくつか動作を確認しました。

### 6. 環境設定の保存

今までのチュートリアルでは毎回DBのURLとドライバの情報をコマンドに含めていましたが、`atlas.hcl`という名前のファイルに環境設定を入れておくことで都度URL等を指定する必要がなくなります。
また、ファイル内で環境変数を指定し実行時に埋め込むことが可能です。
例えば上記のように環境設定を指定することで、環境変数DB_PASSを用いてパスコードを指定することが可能となります。

```atlas.hcl
// https://atlasgo.io/atlas-schema/projects
env "local" {
  // Declare where the schema definition resides.
  // Also supported: ["file://multi.hcl", "file://schema.hcl"].
  src = "file://schema_users_and_blogposts.hcl"

  // Define the URL of the database which is managed
  // in this environment.
  url = "mysql://root:{DB_PASS}@localhost:3306/example"

  // Define the URL of the Dev Database for this environment
  // See: https://atlasgo.io/concepts/dev-database
  dev = "docker://mysql/8/dev"
}
```

実行してみましょう。事前に`export DB_PASS=pass`にてパスワードを指定しておきます。
`atlas`コマンドを実行する際には今までのように引数としてURL等は指定せず、`atlas.hcl`で定義した`local`環境を指定するのみです。

```
export DB_PASS=pass
atlas schema apply --env local
# -> Schema is synced, no changes to be made
```

上記の実行結果から、正常にスキーマとDBの状態が読み取れ比較されていることが分かります。

### 7. スキーマファイルの分割

大規模なプロジェクト等では1つのスキーマのなかにたくさんのテーブル定義が含まれることから、スキーマの定義ファイルを1つにすると非常に長くなってしまうことが想定されます。
先ほど作成した環境設定には複数のスキーマファイルを指定することが出来るため、環境設定にて指定しておくことでファイルの分割が可能です。例えば、`authors.hcl`というファイルを作成し、これを先ほど作成した環境設定`atlas.hcl`に追加します。

```sql:authors.hcl
table "authors" {
  schema = schema.example
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = false
    type = varchar(100)
  }
  column "age" {
    null = true
    type = int
  }
  primary_key {
    columns = [column.id]
  }
}
```

```diff hcl:atlas.hcl
// https://atlasgo.io/atlas-schema/projects
env "local" {
  // Declare where the schema definition resides.
  // Also supported: ["file://multi.hcl", "file://schema.hcl"].
-  src = ["file://schema_users_and_blogposts.hcl"]
+  src = ["file://schema_users_and_blogposts.hcl", "file://schema_authors.hcl"]

  // Define the URL of the database which is managed
  // in this environment.
  url = "mysql://root:pass@localhost:3306/example"

  // Define the URL of the Dev Database for this environment
  // See: https://atlasgo.io/concepts/dev-database
  dev = "docker://mysql/8/dev"
}
```

この状態で`atlas apply`してみるとと、作成した`authors.hcl`も認識さていることが分かります。

```bash
atlas schema apply --env local 
```

```bash:実行結果
-- Planned Changes:
-- Create "authors" table
CREATE TABLE `authors` (
  `id` int NOT NULL,
  `name` varchar(100) NOT NULL,
  `age` int NULL,
  PRIMARY KEY (`id`)
) CHARSET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
? Are you sure?: 
  ▸ Apply
    Lint and edit (requires login)
    Abort
```

:::message
今回試していませんが、環境設定を使用せずCLIのみで実施する場合にも--toオプションを複数使うことで同様のスキーマ定義分割が可能となるかもしれません。
ただし、実行ミスを防ぐためにも複数のファイルを扱う場合は環境設定ファイルを利用することが望ましいと考えられます。
:::

## おわりに

Go製DBスキーマ管理ツールのAtlasを試してみました！
特定のプログラミング言語のランタイム言語に使いやすいツールなので、ぜひとも試してみてください！


## 今回試せなかった/試さなかったこと

時間の関係やドキュメントが見つからなかったことから自分では試せなかったことを記載しておきます。
良い記事があればリンク記載させていただきたい&試してみたいので教えてください。

#### 1. CICDでAtlasを利用する
AtlasはCICDでのスキーマ管理も得意としています。詳しくはAtlas公式から記事が出ているのでこちらを参照してください。

https://atlasgo.io/guides/modern-database-ci-cd

#### 2. Terraformでのスキーマ管理
Terraformのプロバイダーも提供されており、Terraformを用いてDBスキーマの管理が可能となっています。
試さなかったですがterraformを使ってスキーマを管理しつつ、local環境とdev環境で環境設定を切り替えるような使い方をする場合には少々設定が大変そうと感じました。特段制約が無ければ`terraform`ではなく`atlas`コマンドを利用することも視野に入れ事前に比較検討すべきかと思います。
詳しくはこちらの記事が参考になりそうです。

https://qiita.com/ganta/items/f3ba2cba775e228162f2


#### 4. 自作ドライバでのスキーマ管理
**External Schemas**という機能でGo等の言語のORMと連携できる様子です。

https://atlasgo.io/blog/2023/06/28/external-schemas-and-gorm-support

#### 5. PostgreSQLにおけるDatabaseとSchemaの指定
今回はMySQLだったのでdatabase=schema(=今回はexampleを指定)のみを指定しました。
postgreSQLだとdatabaseのなかにschemaがあり、その中にtableがある構造となるはずなので、スキーマファイルやCLI上での指定がどう変わるか検証しておきたかったです。

特に、"コストを抑えつつローカル環境構築コストを抑えるためクラウド上にローカル開発用のDBインスタンスは1つ構築し、個人ごとにdatabaseを作成して使用する"といった使い方をする場合にはdatabaseの指定だけ切り替えてschema以下の指定は同じするようなモチベーションはあるはずです。

少なくとも環境設定`atlas.hcl`のなかで環境変数を使ってデータベースのみを切り替えつつ各スキーマ、テーブルの定義をPostgreSQLに反映するようなことはできるのではないかと感じるので、上記ユースケースにおいてもAtlasを利用することはできそうと感じます。

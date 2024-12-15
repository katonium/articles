---
title: "Terraformに入門した"
emoji: "🌏"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["terraform", "tech"]
published: false
---

## はじめに

Terraformのチュートリアルをようやくざっと流してきたので自分の理解の整理も兼ねて記事にしてみました。

https://developer.hashicorp.com/terraform/tutorials

## まず思ったことを雑多にメモする

- プロバイダーという概念がある。AWS, GCP, Azure, Docker, MySQL, etc.
- Docker用のプロバイダもあり、Dockerのリソースを管理できる（コンテナ・イメージの両方）
  - K8sのリソースも管理できるとかになったらYaml書かなくていいから楽なのかもなーと思った
    - と思ったらあった[terraform-provider-kubernetes](https://github.com/hashicorp/terraform-provider-kubernetes)
      - あんまり使われていない気がするので、なにか難しいところがある？
    - 中身はGoのコードっぽい。インターフェースだけTerraformが提供しているものを満たせばOKなのかな？と感じたので、自分でもなにか作ろうと思えば作れそう。

```bash
$ terraform --version
Terraform v1.10.1
on linux_amd64
```

とりあえず最も基本なDockerコンテナのリソース管理から

```hcl:main.tf
terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
      version = "~> 3.0.1"
    }
  }
}

provider "docker" {}

resource "docker_image" "nginx" {
  name         = "nginx:latest"
  keep_locally = false
}

resource "docker_container" "nginx" {
  image = docker_image.nginx.image_id
  name  = "tutorial"
  ports {
    internal = 80
    external = 8000
  }
}
```

- Providerにもバージョンの概念がある
- `source = "kreuzwerker/docker"` は `https://registry.terraform.io/providers/kreuzwerker/docker/latest`のことを指しているっぽい？
  - dockerみたいにURLでちゃんと指定すれば別のレジストリにも取りに行ける？
- providerブロックでdockerを使うことを示していそう、terraformブロックはインポートするプロバイダーのバージョンを指定しているだけ？
  - providerブロックを複数定義するとどうなるんだろうか。逆に使っていないterraform.required_providerブロックがある分には大丈夫？
- `resource`ブロックはその名の通りリソースを定義しているっぽい
  - `docker_image`と`docker_container`はそれぞれDockerイメージとコンテナを管理するリソース
  - ↑のあとに続く`nginx`は自分で名称変えてOK？
    - 試してみたけど、
- nameはコンテナ名称っぽい

色々考えてたけど、説明があった

> The terraform {} block contains Terraform settings, including the required providers Terraform will use to provision your infrastructure. For each provider, the source attribute defines an optional hostname, a namespace, and the provider type. Terraform installs providers from the Terraform Registry by default. In this example configuration, the docker provider's source is defined as kreuzwerker/docker, which is shorthand for registry.terraform.io/kreuzwerker/docker.
> 
> You can also set a version constraint for each provider defined in the required_providers block. The version attribute is optional, but we recommend using it to constrain the provider version so that Terraform does not install a version of the provider that does not work with your configuration. If you do not specify a provider version, Terraform will automatically download the most recent version during initialization.
> 
> To learn more, reference the provider source documentation.

terraformブロックは依存関係を定義するブロックで合っていそう。デフォルトではTerraform Registryからプロバイダーをインストールするっぽいので、それ以外からも出来そう。

> You can use multiple provider blocks in your Terraform configuration to manage resources from different providers. You can even use different providers together. For example, you could pass the Docker image ID to a Kubernetes service.

providerブロックはプロバイダーを使うことを示すブロックで合っていそう。複数定義しても大丈夫そう。たしかにDockerとKubernetesは同時に使われること多そうだし、共存できるのは便利そうかつ理にかなっていそう。


考えるだけじゃなく、とりあえず動かさないことにははじまらない

```bash
$ terraform init
```

## AWS編

必要な環境変数をセット

```bash
export AWS_ACCESS_KEY_ID=
export AWS_SECRET_ACCESS_KEY=
```


```hcl:main.tf
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.16"
    }
  }

  required_version = ">= 1.2.0"
}

provider "aws" {
  region  = "us-west-2"
}

resource "aws_instance" "app_server" {
  ami           = "ami-830c94e3"
  instance_type = "t2.micro"

  tags = {
    Name = "ExampleAppServerInstance"
  }
}
```

このあたりのコマンドで静的解析できる

```
terraform fmt
terraform validate
```

Error: configuring Terraform AWS Provider: validating provider credentials: retrieving caller identity from STS: operation error STS: GetCallerIdentity, https response error StatusCode: 403, RequestID: 3863c275-c074-47ef-968b-91276c3dcdbd, api error InvalidClientTokenId: The security token included in the request is invalid.

TF_VAR_で環境変数を明示的に渡せるので、試してみる。ちなみに細かいけど環境変数の大文字と小文字は区別されるらしい。

```bash
export TF_VAR_aws_secret_access_key=$AWS_SECRET_ACCESS_KEY
export TF_VAR_aws_access_key_id=$AWS_ACCESS_KEY_ID
```

コードも修正

```hcl:main.tf
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.16"
    }
  }

  required_version = ">= 1.2.0"
}

+variable "aws_access_key_id" {
+  type = string
+}
+
+variable "aws_secret_access_key" {
+  type = string
+}
+
provider "aws" {
  region     = "us-west-2"
+  access_key = var.aws_access_key_id
+  secret_key = var.aws_secret_access_key
}

resource "aws_instance" "app_server" {
  ami           = "ami-830c94e3"
  instance_type = "t2.micro"

  tags = {
    Name = "ExampleAppServerInstance"
  }
}
```

同じエラー引いたので、そもそもAWS側の問題っぽい。

⇒IAMのFBA設定を削除したら接続できるようになった。Providerに明示的にキーを渡せるのはアカウント使い分けとかのために便利そう。

リージョンを東京に修正

⇒`ami-830c94e3`がないって言われたのでAmazon Linux 2のAMI(`ami-0166fe664262f664c`)に差し替え。

⇒と思ったらこっちでもエラーが起きた。リージョンによってAMI IDが異なっている様子。もとのリージョンに戻すと`ami-830c94e3`でも成功した。改めて東京リージョンのAMI `ami-0037237888be2fe22`でインスタンスを作り直す。

リージョンを変えたからか、作成済みのインスタンスが表示されなかった。リージョンを戻してAMIだけ変えると2 to add, 2 to destroyになったのでリージョンごとにリソースが管理されるっぽい。グローバルリソースの場合は表示される気はする。

```hcl:main.tf
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.16"
    }
  }

  required_version = ">= 1.2.0"
}

provider "aws" {
  region = "us-west-2"
  # region = "ap-northeast-1"
}

resource "aws_instance" "app_server" {
  ami           = "ami-0037237888be2fe22"
  instance_type = "t2.micro"

  tags = {
    Name = "ExampleAppServerInstance"
  }
}

resource "aws_instance" "database_server" {
  ami           = "ami-0037237888be2fe22"
  instance_type = "t2.micro"


  tags = {
    Name = "ExampleAppServerInstance"
  }
}
```

動かない状態でapplyしてみたら、destoroyは成功、createは失敗となった。この場合、Destoroyは成功しているが既存インスタンスが再度作成されるような挙動にはならない（=ロールバック機構はない）。

リソースが再作成される場合、順序の指定はできるのだろうか？既存インスタンスが先に削除されると最悪サービスストップとかにもつながりかねないので、若干危ないなと感じた。（実体としては大規模な変更を加える際には先に新規リソース作成、既存リソースからトラフィックを移す、既存リソース削除といった流れで複数回Applyすることが想定されるので、問題とはならない気もするが）


## リモートにtfstateを配置する





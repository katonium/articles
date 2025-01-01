---
title: "TerraformインストールからGoogle Cloud上のリソースをGitHub Actionsで管理するまで"
emoji: "🌏"
type: "tech" # tech: 技術記事 / idea: アイデア
topics: ["terraform", "googlecloud", "githubactions", "workloadidentity", "tech"]
published: true
---

## はじめに

- 本記事ではWorkload Identityを使ってGoogle Cloud上のリソースをGitHub Actions × Terraformで管理する方法を紹介します。
- 新年一発目のハンズオンのお題として、Terraform × Google Cloud × GitHub Actionsはいかがでしょうか！

### 本記事で作るもの

- Terraformのtfstateを保存するCloud StorageバケットをTerraformで作成
- Workload Identityを使ったGitHub ActionsでのTerraformの実行用リソースの作成
- GitHub ActionsでTerraformを実行するためのワークフローの作成

### 本記事でやらないこと

下記の事項は本記事では取り扱いません。

- 複数のtfstateを管理する方法
- Atlantisに代表される `merge-after-apply` なPRの実行
- OIDC認証の説明

### 本記事の流れ

本記事は下記の流れで進めていきます。

0. ワークスペースの設定
1. Terraformのインストール・初期化
2. tfstate用のバケットの作成・tfstateの移行
3. Workload Identityを使ったGitHub ActionsでのTerraformの実行用リソースの作成
4. GitHub Actions用のファイル作成
5. 作成したGoogle Cloudリソースの削除

それではいきましょう！

## 0. ワークスペースの設定

ハンズオンにおいて事前に準備しておいてほしい事項を紹介します。今回の主題ではないため、基本的にはリンクを紹介するに留めます。

::::details ワークスペースの設定

### 0-1. 空のGitHubリポジトリの作成・クローン・チェックアウト

下記を参考に、新しいリポジトリを作成してください。
README.mdを作成しておくオプションにチェックを付けておくと良いでしょう。

https://docs.github.com/ja/repositories/creating-and-managing-repositories/creating-a-new-repository

作成したリポジトリをクローンしてください。下記のリンクを参考に、GitHub Codespacesを使ってクローンすることも可能です。便利なのでおすすめです。

https://zenn.dev/yuhei_fujita/articles/github-codespaces-introduction

クローンしたリポジトリで任意のブランチを作成しチェックアウトしてください。

```bash
git checkout -b `任意のブランチ名`
```

### 0-2. Google Cloudのプロジェクトの作成・認証情報の取得

下記のリンクの1,2の手順に沿ってGoogle Cloudのプロジェクトを作成します。

https://dev.classmethod.jp/articles/google-cloud-start/

:::message
プロジェクトの作成において、クレジットカードの登録が必要です。
本記事で作成するリソースは無料枠内で作成している想定のため、基本的に料金は発生しませんが、金額が0であることは検証しておりません。課金が発生する場合もありますのでご注意ください。また継続利用がない場合、作成したリソースは最後に削除することをお勧めします。
:::

### 0-3. Google Cloud CLIのインストール

Google Cloud CLIをインストールしてください。
いちおうコマンド貼っておきますが、バージョン古い気がするので下記リンクを参考に最新版をインストールしてください。

https://cloud.google.com/sdk/docs/install

```bash
curl -O https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz
tar -xf google-cloud-cli-linux-x86_64.tar.gz
bash google-cloud-sdk/install.sh 
rm google-cloud-cli-linux-x86_64.tar.gz
```

:::message
コマンドを実行したディレクトリ配下に `google-cloud-sdk/` フォルダが作成されGoogle Cloud CLIがインストールされます。
:::

セットアップしたら、認証情報を設定してください。
今回はプロジェクトを作成したGoogleアカウントで認証します。

```bash
gcloud auth login
gcloud config set project `作成したプロジェクトID`
gcloud auth application-default login
```

`gcloud auth application-default login` を実行すると、Google Cloud SDKの認証情報を使用してTerraformの実行を行うための認証情報が作成されます。

::::

## 1. Terraformのインストール・初期化

基本は下記を参考にインストールすればよいです。

https://developer.hashicorp.com/terraform/install


今回は下記のコマンドでインストールします。
どこから見つけたコマンドか忘れましたが、この入れ方が一番簡単でした。
バージョンは2025/1/1時点で最新の1.10.3を選択。

```
export VERSION=1.10.3
wget -O terraform_${VERSION}_linux_amd64.zip https://releases.hashicorp.com/terraform/${VERSION}/terraform_${VERSION}_linux_amd64.zip
unzip terraform_${VERSION}_linux_amd64.zip
sudo mv terraform /usr/local/bin/
rm terraform_${VERSION}_linux_amd64.zip
```

インストールできたことを `terraform --version` で確認。

```bash
terraform --version
# Terraform v1.10.3
# on linux_amd64
```

## 2. tfstate用のバケットの作成・tfstateの移行

下記のチュートリアルを参考に、Google Cloud Storageにtfstateを保存するためのバケットを作成します。

https://cloud.google.com/docs/terraform/resource-management/store-state


:::message
今後も使用する際にはこのバケットにはTerraformによる削除からの保護設定(lifecycle` ブロックでの設定)を入れることを強くお勧めします。
今回は終了後にクラウド上のリソースを全て削除する予定であるため、コメントアウトしています。
:::

```hcl:main.tf
variable "project_id" {
  type        = string
  description = "Google Cloud Project ID"
  default     = "`作成したプロジェクトID`"
}

provider "google" {
  project = var.project_id
  region  = "asia-northeast1"
}

resource "random_id" "default" {
  byte_length = 2
}

resource "google_storage_bucket" "default" {
  name     = "terraform-remote-backend-${random_id.default.hex}"
  location = "ASIA-NORTHEAST1"

  force_destroy               = false
  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true

  versioning {
    enabled = true
  }
  # # Cloud Storageのバケットの削除からの保護設定
  # lifecycle {
  #   prevent_destroy = true
  # }
}

resource "local_file" "default" {
  file_permission = "0644"
  filename        = "${path.module}/backend.tf"

  content = <<-EOT
  terraform {
    backend "gcs" {
      bucket = "${google_storage_bucket.default.name}"
    }
  }
  EOT
}
```

`terraform init` を実行。

```bash
terraform init
```

その後、`terraform apply` を実行。

```bash
terraform apply
```

結果として、下記のようにバケット・ランダム文字列・ファイルの3つのリソースが生成されることを示すメッセージが出ます。

確認し、問題なければ `yes` を入力してバケットを作成します。

:::details terraform apply の結果
```bash
$ terraform apply

Terraform used the selected providers to generate the following execution plan. Resource actions are
indicated with the following symbols:
  + create

Terraform will perform the following actions:

  # google_storage_bucket.default will be created
  + resource "google_storage_bucket" "default" {
      + effective_labels            = {
          + "goog-terraform-provisioned" = "true"
        }
      + force_destroy               = false
      + id                          = (known after apply)
      + location                    = "ASIA-NORTHEAST1"
      + name                        = (known after apply)
      + project                     = (known after apply)
      + project_number              = (known after apply)
      + public_access_prevention    = "enforced"
      + rpo                         = (known after apply)
      + self_link                   = (known after apply)
      + storage_class               = "STANDARD"
      + terraform_labels            = {
          + "goog-terraform-provisioned" = "true"
        }
      + uniform_bucket_level_access = true
      + url                         = (known after apply)

      + soft_delete_policy (known after apply)

      + versioning {
          + enabled = true
        }

      + website (known after apply)
    }

  # local_file.default will be created
  + resource "local_file" "default" {
      + content              = (known after apply)
      + content_base64sha256 = (known after apply)
      + content_base64sha512 = (known after apply)
      + content_md5          = (known after apply)
      + content_sha1         = (known after apply)
      + content_sha256       = (known after apply)
      + content_sha512       = (known after apply)
      + directory_permission = "0777"
      + file_permission      = "0644"
      + filename             = "./backend.tf"
      + id                   = (known after apply)
    }

  # random_id.default will be created
  + resource "random_id" "default" {
      + b64_std     = (known after apply)
      + b64_url     = (known after apply)
      + byte_length = 2
      + dec         = (known after apply)
      + hex         = (known after apply)
      + id          = (known after apply)
    }

Plan: 3 to add, 0 to change, 0 to destroy.

Do you want to perform these actions?
  Terraform will perform the actions described above.
  Only 'yes' will be accepted to approve.

  Enter a value:
```
:::

apply完了後、下記のファイルが作成されていることが確認できます(バケット名末尾の `36ca` はランダム文字列です)。このファイルは、Terraformの実行時にtfstateを保存するバケットを指定するために使用します。今までこの設定はなかったため、現在tfstateはデフォルト設定であるローカルディレクトリに保存されています。`terraform.tfstate` ってやつです。

```hcl:backend.tf
terraform {
  backend "gcs" {
    bucket = "terraform-remote-backend-36ca"
  }
}
```

続いて、`terraform.tfstate` を新しく作成されたバケットに移行します。既に設定自体は完了しているため、`terraform init --migrate-state` を実行してください。

```bash
terraform init --migrate-state
```

`Do you want to copy existing state to the new backend?` というメッセージが出るので、`yes` を入力してtfstateを移行します。

`terraform.tfstate` がローカルディレクトリから削除されています。Cloud Storageのバケットに移行されたことを確認します。

![Cloud Storageバケット](/images/terraform-googlecloud-starter-kit-cloud-storage.png)

これで、tfstateの保存先の設定は完了です。

## 3. Workload Identityを使ったGitHub ActionsでのTerraformの実行用リソースの作成

続いて、Workload Identityを使ったGitHub ActionsでのTerraformの実行用リソースの作成を行います。

このセクションのコードは下記のサイトを参考にしました。

https://dev.classmethod.jp/articles/google-cloud-auth-with-workload-identity/

https://github.com/terraform-google-modules/terraform-google-github-actions-runners/tree/master/examples/oidc-simple

https://cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines#github-actions_2




Workload Identityを使うことで、GitHub ActionsでTerraformを実行するためにアクセス用のキーを発行する必要がなくなります。GitHubとGoogle Cloudの間で短時間しか有効でないトークンを発行して認証することで、認証情報が漏洩するリスクを減らすことができます。

`oidc.tf` を作成し、下記のコードを記述してください。

:::message
リポジトリ名・グループ名・ユーザー名の部分は実際に試すリポジトリに合わせて変更してください。
:::

```hcl:oidc.tf
# OIDC認証用のプールとプロバイダーを作成
module "oidc" {
  source  = "terraform-google-modules/github-actions-runners/google//modules/gh-oidc"
  version = "~> 4.0"

  pool_id             = "example-github-actions-pool"
  provider_id         = "example-github-actions-provider"
  attribute_condition = "assertion.repository_owner=='`リポジトリのグループ名 or ユーザー名`'"
  issuer_uri          = "https://token.actions.githubusercontent.com"
  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.actor"      = "assertion.actor"
    "attribute.repository" = "assertion.repository"
  }
  sa_mapping = {
    (google_service_account.github_actions_sa.account_id) = {
      sa_name   = google_service_account.github_actions_sa.name
      attribute = "attribute.repository/`リポジトリのグループ名 or ユーザー名`/`リポジトリ名`"
    }
  }
}

# サービスアカウントを作成
resource "google_service_account" "github_actions_sa" {
  account_id   = "github-actions-sa"
  display_name = "GitHub Actions Service Account"
}

# サービスアカウントに必要なロールを追加
# 今回は最低限のロールのみ追加しています。変更するリソースに応じて適切なロールを追加してください。
resource "google_project_iam_member" "github_actions_sa_serviceAccountUser" {
  project = var.project_id
  role    = "roles/iam.serviceAccountUser"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_storageObjectAdmin" {
  project = var.project_id
  role    = "roles/storage.objectAdmin"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_viewer" {
  project = var.project_id
  role    = "roles/viewer"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_workloadIdentityUser" {
  project = var.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}

# ワークフロー実行のために必要なAPIを有効化
resource "google_project_service" "iamcredentials_api" {
  project = var.project_id
  service = "iamcredentials.googleapis.com"
}
resource "google_project_service" "iam_api" {
  project = var.project_id
  service = "iam.googleapis.com"
}
resource "google_project_service" "cloudresourcemanager_api" {
  project = var.project_id
  service = "cloudresourcemanager.googleapis.com"
}
```

また、作成されたリソースの名称をGitHub Actionsのワークフローで使用するため、下記のように `outputs.tf` を作成してください。

```hcl:outputs.tf
output "provider_name" {
  description = "Provider name"
  value       = module.oidc.provider_name
}

output "sa_email" {
  description = "Example SA email"
  value       = google_service_account.github_actions_sa.email
}
```

ここまで出来たら、OIDCモジュールが新しく追加されているため再度 `terraform init` を実行してください。

```bash
terraform init
```

その後、`terraform apply` を実行してOIDCモジュールおよびGitHub Actions用のサービスアカウントを作成してください。

```bash
terraform apply
```

作成完了後、`Outputs` に下記のような値が出力されていることを確認してください。

```bash
Outputs:

provider_name = "projects/xxxxxxxxxxx/locations/global/workloadIdentityPools/example-github-actions-pool/providers/example-github-actions-provider"
sa_email = "github-actions-sa@xxxxxxxxxxx.iam.gserviceaccount.com"
```

これらの値をGitHub Actionsのワークフローで使用します。

## 4. GitHub Actions用のファイル作成

続いて、GitHub Actions用のワークフローを作成します。
このセクションの執筆においては、下記のサイトを参考にしました。

https://developer.hashicorp.com/terraform/tutorials/automation/github-actions

https://cloud.google.com/blog/ja/products/identity-security/enabling-keyless-authentication-from-github-actions

`.github/workflows/terraform.yml` を作成し、下記のコードを追加してください。

:::message
`provider_name` と `sa_email` は、前節の `terraform apply` の結果から出力された値を使用してください。
:::


```yaml:.github/workflows/terraform.yml
name: "Terraform"

on:
  push:
    branches:
      - main
  pull_request:

jobs:
  terraform:
    name: "Terraform"
    runs-on: ubuntu-latest
    permissions:
      pull-requests: write
      contents: read
      id-token: write
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - id: auth
        name: 'Authenticate to Google Cloud'
        uses: google-github-actions/auth@v0.4.0
        with:
          # ここに前節の `provider_name` を設定
          workload_identity_provider: 'projects/xxxxxxxxxxxx/locations/global/workloadIdentityPools/example-github-actions-pool/providers/example-github-actions-provider'
          # ここに前節の `sa_email` を設定
          service_account: 'github-actions-sa@xxxxxxxxxxxx.iam.gserviceaccount.com'

        # ここは実際には不要。今回のコードでは認証が正しく成功しているか確認するために追加している。
      - name: Set up Cloud SDK
        uses: google-github-actions/setup-gcloud@v2.1.2

        # ここは実際には不要。今回のコードでは認証が正しく成功しているか確認するために追加している。
      - name: Describe Service Account
        # ここに前節の `sa_email` を設定
        run: gcloud iam service-accounts describe 'github-actions-sa@xxxxxxxxxxxx.iam.gserviceaccount.com'
  
      - name: Setup Terraform
        uses: hashicorp/setup-terraform@v1

      - name: Terraform Format
        id: fmt
        run: echo $pwd && terraform fmt -check

      - name: Terraform Init
        id: init
        run: terraform init
      
      - name: Terraform Validate
        id: validate
        run: terraform validate -no-color

      - name: Terraform Plan
        id: plan
        if: github.event_name == 'pull_request'
        run: terraform plan -no-color -input=false
        continue-on-error: true

      - name: Update Pull Request
        uses: actions/github-script@v6
        if: github.event_name == 'pull_request'
        env:
          PLAN: ${{ steps.plan.outputs.stdout }}
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          script: |
            const output = `#### Terraform Format and Style 🖌\`${{ steps.fmt.outcome }}\`
            #### Terraform Initialization ⚙️\`${{ steps.init.outcome }}\`
            #### Terraform Validation 🤖\`${{ steps.validate.outcome }}\`
            #### Terraform Plan 📖\`${{ steps.plan.outcome }}\`

            <details><summary>Show Plan</summary>

            \`\`\`terraform\n
            ${process.env.PLAN}
            \`\`\`

            </details>

            *Pushed by: @${{ github.actor }}, Action: \`${{ github.event_name }}\`*`;

            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: output
            })

      - name: Terraform Plan Status
        if: steps.plan.outcome == 'failure'
        run: exit 1

      - name: Terraform Apply
        if: github.ref == 'refs/heads/main' && github.event_name == 'push'
        run: terraform apply -auto-approve -input=false

```

コミットし、Pushします。


## 5. ワークフローの実行

このワークフローは `pull_request` イベントが発生した際に実行されます。GitHub上でこのブランチからメインブランチへのPRを作成し、実行されることを確認します。
うまくいっていると、下記のようにPRへとコメントが追加されていることが確認できます！

`Show Plan` をクリックすると、Terraformのplan結果が確認できます。

![PRへのコメント](/images/terraform-googlecloud-starter-kit-pr-result.png)

:::message
ワークフローの実行時に下記のようなエラーが発生することがあります。特定のAPIが有効になっていないことが原因なので、エラーメッセージに示されているGoogle Cloudコンソールへのリンクへアクセスし、APIを有効にしてください。

```
ERROR: (gcloud.iam.service-accounts.describe) There was a problem refreshing your current auth tokens: ('Unable to acquire impersonated credentials', '{\n  "error": {\n    "code": 403,\n    "message": "IAM Service Account Credentials API has not been used in project xxxxxxxxxx before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/iamcredentials.googleapis.com/overview?project=xxxxxxxxxx then retry. ....
```

:::

PRへのコメントは `terraform plan` の結果ですが、マージすると `terraform apply` されるため、Plan結果を確認しマージすることでインフラリソースのコントロールが可能となります。

## 6. 作成したGoogle Cloudリソースの削除

最後に、作成したGoogle Cloudリソースを削除します。

```bash
terraform destroy
```

お疲れ様でした！


## 参考

下記のサイトを参考に記事を作成しています。文中で紹介しているものもありますが、紹介していないものとまとめて再掲します。


https://cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines#github-actions_2

https://cloud.google.com/docs/terraform/resource-management/store-state

https://developer.hashicorp.com/terraform/tutorials/gcp-get-started


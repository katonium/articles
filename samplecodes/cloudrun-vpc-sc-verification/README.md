# VPC-SC × Cloud Run Jobs × Serverless VPC Connector 検証

`cloudbuild-private-pool` で作った VPC-SC 境界 + Shared VPC を **そのまま再利用** して、 Cloud Run V2 Jobs + Serverless VPC Access Connector の挙動を 5 パターン検証する。

## 依存関係

このモジュールは **`../cloudbuild-private-pool/` を先に `terraform apply` した前提** で動く。 base / guest_a / guest_b の各プロジェクト・Shared VPC・Service Perimeter A/B はすべて cloudbuild 側で構築済みであることを期待し、 ID は tfvars で受け取る (data source 経由で参照)。

Terraform state は **完全に独立した別 state**。 apply / destroy の境界が cloudbuild 側と分離されているので、 Cloud Run 検証を撤収しても cloudbuild 側の検証環境は残る。

## Perimeter の `restricted_services` 追加について

本モジュールは perimeter を mutate しない。 `run.googleapis.com` / `vpcaccess.googleapis.com` / `storage.googleapis.com` / `bigquery.googleapis.com` を境界内で保護したい場合、 **cloudbuild-private-pool 側の `perimeter_restricted_services` tfvars に追加してから apply し直す** こと。 例:

```hcl
perimeter_restricted_services = [
  "artifactregistry.googleapis.com",
  "bigquery.googleapis.com",
  "cloudbuild.googleapis.com",
  "compute.googleapis.com",
  "containerregistry.googleapis.com",
  "iam.googleapis.com",
  "logging.googleapis.com",
  "monitoring.googleapis.com",
  "pubsub.googleapis.com",
  "run.googleapis.com",
  "secretmanager.googleapis.com",
  "servicenetworking.googleapis.com",
  "serviceusage.googleapis.com",
  "storage.googleapis.com",
  "vpcaccess.googleapis.com",
]
```

## 構成

| Case | Job 配置 project | VPC Connector | アクセス先 (GCS / BQ) | 期待 |
|------|---|---|---|---|
| **C0** ベース | base (Perim A) | base 内 | base/Perim A | SUCCESS |
| **C2** Shared VPC, cross-project | guest_a (Perim A) | guest_a → host VPC | base/Perim A (host) | SUCCESS (same-perim) |
| **C3** Shared VPC, in-project | guest_a (Perim A) | guest_a → host VPC | guest_a/Perim A | SUCCESS |
| **C4** 境界分離, host 参照 | guest_b (Perim B) | guest_b → host VPC | base/Perim A (host) | NOT SUCCESS (cross-perim deny) |
| **C5** 境界分離, 自リソース | guest_b (Perim B) | guest_b → host VPC | guest_b/Perim B | NOT SUCCESS (cross-perim Shared VPC により connector が機能しない想定) |

Case 1 (perimeter 外の project へのアクセス) は本検証スコープ外 (フォルダ外プロジェクトが必要)。

### Case 4 / 5 の deny 経路について

Case 4 (cross-perim, host resource) の deny は VPC-SC 由来。 一方 Case 5 は同一 perim 内の自リソースを叩いているにも関わらず SUCCESS しないことを期待している。 理由は guest_b の Serverless VPC Connector が **Shared VPC host (=base, Perim A)** の subnet を消費するため、 cross-perim Shared VPC が成立せず connector の network が立ち上がらない、と推測している。 これは cloudbuild-private-pool 側の Case 4 (`TestCase4_SplitPerimeter_GuestPool_PullGuestAR`) と同じ構造の現象。

Case 4 では IAM (host resource の reader) をあえて付与していない (VPC-SC レイヤーの deny を観測するため)。 Case 5 では IAM は guest_b 自リソースに対して通っているので、 仮に connector が動けば成功する状況。 つまり Case 5 が失敗するのは「IAM 由来」ではなく「networking 由来」になる。

## ファイル構成

```
samplecodes/cloudrun-vpc-sc-verification/
├── 00_providers.tf              google / google-beta provider
├── 01_variables.tf              tfvars 入力 (project / perimeter / suffix etc.)
├── 02_apis.tf                   3 project × 必要 API 有効化 + data.google_project x 3
├── 03_vpc_connectors.tf         /28 subnet × 3 + VPC Connector × 3 + Shared VPC IAM
├── 04_cloud_run_jobs.tf         google_cloud_run_v2_job × 5 (Case 0/2/3/4/5)
├── 05_protected_resources.tf    GCS bucket × 3 + BQ dataset+table × 3
├── 06_iam.tf                    Job 実行 SA × 3 + GCS/BQ reader IAM
├── 07_outputs.tf                test 側で読む output
├── Makefile                     init / plan / apply / test / destroy
├── terraform.tfvars.example     コピーして tfvars にする例
├── go.mod / go.sum              Cloud Run V2 Go SDK
├── cloudrun_test.go             5 case の Go test (RunJob → op.Wait → 判定)
└── README.md
```

## probe スクリプト

Cloud Run Job は別 image を build せず、 `gcr.io/google.com/cloudsdktool/cloud-sdk:slim` を直接使い、 `command = ["bash", "-c", <インライン script>]` で probe する。 script は以下のことだけする:

1. `gsutil ls gs://$TARGET_BUCKET/`
2. `bq show $TARGET_PROJECT:$TARGET_DATASET.t`

どちらも非ゼロ終了したら Job も失敗。 成功すれば exit 0。

## 実行手順

```bash
# 1. cloudbuild-private-pool 側を apply 済み・出力を控えておく
cd ../cloudbuild-private-pool
terraform output suffix
terraform output perimeter_a_name
terraform output perimeter_b_name
terraform output base_project_id
terraform output guest_a_project_id
terraform output guest_b_project_id

# 2. このディレクトリで tfvars を埋める
cd -
cp terraform.tfvars.example terraform.tfvars
# エディタで terraform.tfvars を埋める

# 3. terraform apply (新規リソースは VPC Connector × 3、 GCS × 3、 BQ dataset+table × 3、 SA × 3、 Cloud Run Job × 5 etc.)
make init
make plan      # 必ず増減を確認
make apply

# 4. テスト実行
make test
```

## 破棄

```bash
make destroy
```

cloudbuild-private-pool 側はこのモジュールに依存しないので、 こちらを destroy しても問題ない。 cloudbuild 側を destroy する場合は **先にこちら (cloudrun) を destroy しておくこと** (こちらが host VPC や Perimeter を data source で参照しているため)。

## 注意点

- Serverless VPC Connector は **/28 専用 subnet** が必須。 cloudbuild-private-pool の既存 /24 subnet は再利用できないため、本モジュールで `10.60.0.0/28`, `10.60.0.16/28`, `10.60.0.32/28` の 3 本を host VPC 上に新設している。 これは host (base) project にぶら下がる subnet で、 guest_a / guest_b の connector が Shared VPC 経由で参照する形。
- Cloud Run V2 API のクライアントはリージョナルエンドポイント (`<region>-run.googleapis.com:443`) を明示する必要がある。 Cloud Build と同じパターン。
- `vpc_access.egress = "ALL_TRAFFIC"` にしているので、 Google API 行きも含めて connector を経由する。 これにより host VPC の DNS routing (cloudbuild 側で構築済みの `restricted.googleapis.com` ゾーン) と FW ルールが効く。
- Case 4 / 5 の deny 理由を切り分けたいなら、 Cloud Run Execution ログを Logs Explorer から確認する (テスト本体は SUCCESS/NOT-SUCCESS の単純判定のみ)。

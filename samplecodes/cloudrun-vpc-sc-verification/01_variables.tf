# cloudbuild-private-pool 側で先に作った既存リソースを再利用するため、
# 必要な project ID / suffix / perimeter 名は tfvars 経由で受け取る。
# Terraform state はこちらで完全に独立した別 state にする。

variable "project_id" {
  description = <<EOT
Terraform 実行 (= API call) の billing / quota project として使う既存プロジェクト
の ID。検証用 Cloud Run Job / VPC Connector などは base / guest_a / guest_b
の各 project に配置する。本変数は API quota の請求先のみに使う。
通常は cloudbuild-private-pool 側と同じ値 (= base project) を指定する。
terraform.tfvars (gitignored) で指定する。
EOT
  type        = string
}

variable "folder_id" {
  description = "VPC-SC 境界をかけるフォルダの数値 ID。cloudbuild-private-pool と同じ値。"
  type        = string
}

variable "org_id" {
  description = <<EOT
Organization の数値 ID。本モジュールでは Access Policy を新規作成しないが、
リソースパスや IAM の文脈で参照することがあるため受け取っておく。
terraform.tfvars (gitignored) でのみ指定する。
EOT
  type        = string
  sensitive   = true
}

variable "region" {
  description = "GCP リージョン。cloudbuild-private-pool と同一リージョンを指定する。"
  type        = string
  default     = "asia-northeast1"
}

variable "billing_account" {
  description = <<EOT
新規プロジェクトは作らないが、provider 設定や IAM 文脈で参照するため受け取る。
terraform.tfvars (gitignored) でのみ指定する。
EOT
  type        = string
  sensitive   = true
}

variable "base_project_id" {
  description = "cloudbuild-private-pool 側で構築済みの base project ID (Shared VPC host)。terraform.tfvars (gitignored)。"
  type        = string
}

variable "guest_a_project_id" {
  description = "cloudbuild-private-pool 側で構築済みの guest_a project ID (Perimeter A 配下の service project)。"
  type        = string
}

variable "guest_b_project_id" {
  description = "cloudbuild-private-pool 側で構築済みの guest_b project ID (Perimeter B 配下の service project)。"
  type        = string
}

variable "suffix" {
  description = <<EOT
cloudbuild-private-pool 側の `terraform output suffix` の値。
既存 VPC / subnet / 各種リソース名のサフィックスを揃えるために必要。
EOT
  type        = string
}

variable "ingress_identities" {
  description = <<EOT
VPC-SC Service Perimeter の Ingress に登録されている identity リスト
(cloudbuild-private-pool 側と同じ値)。テスト側で API 呼び出しに使う identity が
含まれていることを期待する。terraform.tfvars (gitignored) でのみ指定する。
EOT
  type        = list(string)
  sensitive   = true
}

variable "perimeter_a_name" {
  description = <<EOT
Perimeter A の完全名 (例: accessPolicies/<id>/servicePerimeters/cb_pp_<suffix>)。
cloudbuild-private-pool の `terraform output perimeter_a_name` の値を入れる。
本モジュールは perimeter 自体を mutate しない。README にあるように、
`run.googleapis.com` などは cloudbuild-private-pool 側の
`perimeter_restricted_services` tfvars に事前追加しておく。
EOT
  type        = string
}

variable "perimeter_b_name" {
  description = "Perimeter B の完全名 (cloudbuild-private-pool の `terraform output perimeter_b_name`)。"
  type        = string
}

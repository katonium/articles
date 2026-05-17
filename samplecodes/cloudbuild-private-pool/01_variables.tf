variable "project_id" {
  description = <<EOT
Terraform 実行 (= API call) の billing / quota project として使う既存プロジェクト
の ID。検証用のリソースは base / guest_a / guest_b の各 project に配置する。
base は data.google_project.base で既存プロジェクトを参照する (var.base_project_id)。
本変数は API quota の請求先のみに使う。
terraform.tfvars (gitignored) で指定する。
EOT
  type        = string
}

variable "folder_id" {
  description = "VPC-SC 境界をかけるフォルダの数値 ID。Access Policy / Service Perimeter は本フォルダにスコープされる"
  type        = string
}

variable "base_project_id" {
  description = "既存の base project ID (folder 配下にある既存プロジェクトを使う)。 terraform.tfvars (gitignored) で指定。"
  type        = string
}

variable "org_id" {
  description = <<EOT
Access Policy 作成のため、フォルダの親 Organization の数値 ID。
scoped access policy は parent=organizations/<id> 必須、
scopes=[folders/<folder_id>] で当該フォルダ配下のみに作用する。
terraform.tfvars (gitignored) でのみ指定する。
EOT
  type        = string
  sensitive   = true
}

variable "region" {
  description = "GCP リージョン"
  type        = string
  default     = "asia-northeast1"
}

variable "billing_account" {
  description = <<EOT
新規 project に紐付ける Billing Account ID (XXXXXX-XXXXXX-XXXXXX)。
host-a / guest-a / host-b / guest-b の作成 + billing 紐付けに使う。
terraform.tfvars (gitignored) でのみ指定する。
EOT
  type        = string
  sensitive   = true
}

variable "ingress_identities" {
  description = <<EOT
VPC-SC Service Perimeter の Ingress ポリシーに含めるアイデンティティのリスト。
"user:foo@example.com" / "serviceAccount:bar@PROJECT.iam.gserviceaccount.com" 形式。
terraform.tfvars (gitignored) でのみ指定する。コード/コミットには載せない。
EOT
  type        = list(string)
  sensitive   = true
}

variable "perimeter_restricted_services" {
  description = "Service Perimeter で制限する Google API のリスト"
  type        = list(string)
  default = [
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "compute.googleapis.com",
    "containerregistry.googleapis.com",
    "iam.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "pubsub.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "serviceusage.googleapis.com",
    "storage.googleapis.com",
  ]
}

variable "project_id" {
  description = "VPC-SC 境界に入れる検証用プロジェクトの ID。terraform.tfvars (gitignored) で指定する"
  type        = string
}

variable "folder_id" {
  description = "VPC-SC 境界をかけるフォルダの数値 ID。Access Policy / Service Perimeter は本フォルダにスコープされる"
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

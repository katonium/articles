variable "project_id" {
  type        = string
  description = "GCP プロジェクト ID"
}

variable "region" {
  type        = string
  description = "リージョン (Dataflow / Artifact Registry / GCS バケットで使用)"
  default     = "us-central1"
}

variable "bq_location" {
  type        = string
  description = "BigQuery のロケーション"
  default     = "US"
}

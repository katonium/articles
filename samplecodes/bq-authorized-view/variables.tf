variable "project_id" {
  type        = string
  description = "Google Cloud Project ID"
}

variable "region" {
  type        = string
  description = "Google Cloud region"
  default     = "asia-northeast1"
}

variable "location" {
  type        = string
  description = "BigQuery dataset location"
  default     = "asia-northeast1"
}

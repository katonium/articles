output "base_project_id" {
  value = data.google_project.base.project_id
}

output "guest_a_project_id" {
  value = data.google_project.guest_a.project_id
}

output "guest_b_project_id" {
  value = data.google_project.guest_b.project_id
}

output "region" {
  value = var.region
}

output "suffix" {
  value = var.suffix
}

# VPC Connector
output "connector_base_id" {
  value = google_vpc_access_connector.base.id
}

output "connector_guest_a_id" {
  value = google_vpc_access_connector.guest_a.id
}

output "connector_guest_b_id" {
  value = google_vpc_access_connector.guest_b.id
}

# Job 実行 SA
output "job_base_sa_email" {
  value = google_service_account.job_base.email
}

output "job_guest_a_sa_email" {
  value = google_service_account.job_guest_a.email
}

output "job_guest_b_sa_email" {
  value = google_service_account.job_guest_b.email
}

# Cloud Run Job リソース名 (projects/<id>/locations/<region>/jobs/<name>)
output "case0_base_job_name" {
  value = google_cloud_run_v2_job.case0_base.name
}

output "case2_ga_host_job_name" {
  value = google_cloud_run_v2_job.case2_ga_host.name
}

output "case3_ga_self_job_name" {
  value = google_cloud_run_v2_job.case3_ga_self.name
}

output "case4_gb_host_job_name" {
  value = google_cloud_run_v2_job.case4_gb_host.name
}

output "case5_gb_self_job_name" {
  value = google_cloud_run_v2_job.case5_gb_self.name
}

# 保護対象リソース
output "base_bucket" {
  value = google_storage_bucket.base.name
}

output "base_dataset" {
  value = google_bigquery_dataset.base.dataset_id
}

output "guest_a_bucket" {
  value = google_storage_bucket.guest_a.name
}

output "guest_a_dataset" {
  value = google_bigquery_dataset.guest_a.dataset_id
}

output "guest_b_bucket" {
  value = google_storage_bucket.guest_b.name
}

output "guest_b_dataset" {
  value = google_bigquery_dataset.guest_b.dataset_id
}

# Perimeter 名 (cloudbuild-private-pool から受け取った値をそのまま出力)
output "perimeter_a_name" {
  value = var.perimeter_a_name
}

output "perimeter_b_name" {
  value = var.perimeter_b_name
}

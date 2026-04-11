output "project_id" {
  value = var.project_id
}

output "region" {
  value = var.region
}

output "bucket_name" {
  value = google_storage_bucket.workspace.name
}

output "dataset_id" {
  value = google_bigquery_dataset.verification.dataset_id
}

output "dataflow_worker_sa_email" {
  value = google_service_account.dataflow_worker.email
}

output "template_spec_gcs_path" {
  value = local.template_spec_gcs_path
}

# Go テストはこのマップから (format, lang, pattern) -> table_id を解決する
output "destination_tables" {
  value = {
    for k, _ in local.table_specs : k => google_bigquery_table.destinations[k].table_id
  }
}

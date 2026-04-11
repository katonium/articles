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

# 各 format に対応する YAML パイプラインの GCS パス
output "yaml_pipeline_gcs_paths" {
  value = {
    for k, _ in local.yaml_files :
    k => "gs://${google_storage_bucket.workspace.name}/pipelines/${k}.yaml"
  }
}

# Go テストはこのマップから (format, pattern) -> table_id を解決する
output "destination_tables" {
  value = {
    for k, _ in local.table_specs : k => google_bigquery_table.destinations[k].table_id
  }
}

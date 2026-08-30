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

# 宛先テーブル (静的 15 + dated 3) は Go test が create / drop するため
# ここでは output しない。テーブル一覧とスキーマ定義は Go 側で管理する。

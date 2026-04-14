output "project_id" {
  value = var.project_id
}

output "region" {
  value = var.region
}

output "suffix" {
  value = local.suffix
}

output "worker_pool_id" {
  value = google_cloudbuild_worker_pool.main.id
}

output "network_name" {
  value = google_compute_network.main.name
}

output "sa_a_email" {
  value = google_service_account.sa_a.email
}

output "sa_b_email" {
  value = google_service_account.sa_b.email
}

output "bucket_a_name" {
  value = google_storage_bucket.bucket_a.name
}

output "bucket_b_name" {
  value = google_storage_bucket.bucket_b.name
}

output "ar_repo_name" {
  value = google_artifact_registry_repository.main.name
}

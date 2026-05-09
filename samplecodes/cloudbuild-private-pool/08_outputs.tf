output "base_project_id" {
  value = google_project.base.project_id
}

output "base_project_number" {
  value = google_project.base.number
}

output "guest_a_project_id" {
  value = google_project.guest_a.project_id
}

output "guest_a_project_number" {
  value = google_project.guest_a.number
}

output "guest_b_project_id" {
  value = google_project.guest_b.project_id
}

output "guest_b_project_number" {
  value = google_project.guest_b.number
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

output "build_sa_email" {
  value = google_service_account.build.email
}

output "dockerhub_mirror_repo" {
  value = google_artifact_registry_repository.dockerhub_mirror.repository_id
}

output "pypi_mirror_repo" {
  value = google_artifact_registry_repository.pypi_mirror.repository_id
}

output "build_output_repo" {
  value = google_artifact_registry_repository.build_output.repository_id
}

output "perimeter_a_name" {
  value = google_access_context_manager_service_perimeter.main.name
}

output "perimeter_b_name" {
  value = google_access_context_manager_service_perimeter.secondary.name
}

output "access_policy_name" {
  value = google_access_context_manager_access_policy.folder.name
}

output "project_id" {
  value = var.project_id
}

output "dataset_a_id" {
  value = google_bigquery_dataset.dataset_a.dataset_id
}

output "dataset_b_id" {
  value = google_bigquery_dataset.dataset_b.dataset_id
}

output "dataset_c_id" {
  value = google_bigquery_dataset.dataset_c.dataset_id
}

output "table_confidential_id" {
  value = google_bigquery_table.confidential_data.table_id
}

output "view_public_directory_id" {
  value = google_bigquery_table.authorized_view.table_id
}

output "view_nested_directory_id" {
  value = google_bigquery_table.nested_view.table_id
}

output "user1_email" {
  value = google_service_account.user1.email
}

output "user2_email" {
  value = google_service_account.user2.email
}

output "suffix" {
  value = local.suffix
}

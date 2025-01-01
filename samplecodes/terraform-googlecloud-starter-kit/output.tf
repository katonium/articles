output "provider_name" {
  description = "Provider name"
  value       = module.oidc.provider_name
}

output "sa_email" {
  description = "Example SA email"
  value       = google_service_account.github_actions_sa.email
}

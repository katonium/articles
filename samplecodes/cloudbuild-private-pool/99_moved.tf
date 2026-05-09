# ──────────────────────────────────────────────
# moved blocks: shared_host project が base 役 (Cloud Build mainline) を兼ねる
# 構成にリファクタした際の state migration。
# 旧 google_project.base (suspended) は事前に state rm 済み。
# ──────────────────────────────────────────────

moved {
  from = google_project.shared_host
  to   = google_project.base
}

moved {
  from = google_project_service.enabled["shared_host--accesscontextmanager.googleapis.com"]
  to   = google_project_service.enabled["base--accesscontextmanager.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--artifactregistry.googleapis.com"]
  to   = google_project_service.enabled["base--artifactregistry.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--cloudbuild.googleapis.com"]
  to   = google_project_service.enabled["base--cloudbuild.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--cloudresourcemanager.googleapis.com"]
  to   = google_project_service.enabled["base--cloudresourcemanager.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--compute.googleapis.com"]
  to   = google_project_service.enabled["base--compute.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--dns.googleapis.com"]
  to   = google_project_service.enabled["base--dns.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--iam.googleapis.com"]
  to   = google_project_service.enabled["base--iam.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--iamcredentials.googleapis.com"]
  to   = google_project_service.enabled["base--iamcredentials.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--logging.googleapis.com"]
  to   = google_project_service.enabled["base--logging.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--servicenetworking.googleapis.com"]
  to   = google_project_service.enabled["base--servicenetworking.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--serviceusage.googleapis.com"]
  to   = google_project_service.enabled["base--serviceusage.googleapis.com"]
}

moved {
  from = google_project_service.enabled["shared_host--storage.googleapis.com"]
  to   = google_project_service.enabled["base--storage.googleapis.com"]
}

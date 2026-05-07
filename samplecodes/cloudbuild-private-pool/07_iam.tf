# ──────────────────────────────────────────────
# Build 用カスタム Service Account
#
# Build job の serviceAccount として使う。
# AR reader (mirror pull) + AR writer (push) + log writer を最小付与。
# ──────────────────────────────────────────────

resource "google_service_account" "build" {
  project      = var.project_id
  account_id   = "cb-build-sa-${local.suffix}"
  display_name = "Cloud Build job SA (VPC-SC verify)"
}

# Cloud Build Service Agent (P4SA) が build SA を impersonate するために必要
resource "google_service_account_iam_member" "agent_act_as_build_sa" {
  service_account_id = google_service_account.build.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${local.cb_agent_sa}"
}

resource "google_project_iam_member" "build_sa_log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.build.email}"
}

# DockerHub mirror / PyPI mirror から pull するための reader
resource "google_artifact_registry_repository_iam_member" "build_sa_reader_dockerhub" {
  project    = var.project_id
  location   = google_artifact_registry_repository.dockerhub_mirror.location
  repository = google_artifact_registry_repository.dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build.email}"
}

# Build step.name に AR image を指定した場合、worker の docker daemon は
# Cloud Build Service Agent (P4SA) の credential で pull する。よって P4SA
# にも mirror の reader 権限が必要。
resource "google_artifact_registry_repository_iam_member" "p4sa_reader_dockerhub" {
  project    = var.project_id
  location   = google_artifact_registry_repository.dockerhub_mirror.location
  repository = google_artifact_registry_repository.dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${local.cb_agent_sa}"
}

resource "google_artifact_registry_repository_iam_member" "build_sa_reader_pypi" {
  project    = var.project_id
  location   = google_artifact_registry_repository.pypi_mirror.location
  repository = google_artifact_registry_repository.pypi_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build.email}"
}

# build-output に push するための writer
resource "google_artifact_registry_repository_iam_member" "build_sa_writer_output" {
  project    = var.project_id
  location   = google_artifact_registry_repository.build_output.location
  repository = google_artifact_registry_repository.build_output.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.build.email}"
}

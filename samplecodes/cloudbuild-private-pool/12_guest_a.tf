# ──────────────────────────────────────────────
# guest_a (Perimeter A 配下 / Shared VPC service project / Case 3)
#
# - build SA + P4SA impersonation 許可 + log writer
# - AR 3 種 (DockerHub mirror, PyPI mirror, build-output)
# - AR vpcsc_config = ALLOW (REMOTE upstream fetch のため)
# - AR IAM (build SA / P4SA)
# - Cloud Build private pool (peered_network は host (base) の main VPC)
# ──────────────────────────────────────────────

resource "google_service_account" "build_guest_a" {
  project      = google_project.guest_a.project_id
  account_id   = "cb-build-sa-guest-a-${local.suffix}"
  display_name = "Cloud Build job SA (guest_a, VPC-SC verify)"

  depends_on = [google_project_service.enabled]
}

resource "google_service_account_iam_member" "guest_a_agent_act_as_build_sa" {
  service_account_id = google_service_account.build_guest_a.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${local.cb_agent_sa["guest_a"]}"
}

resource "google_project_iam_member" "guest_a_build_sa_log_writer" {
  project = google_project.guest_a.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.build_guest_a.email}"
}

# ──────────────────────────────────────────────
# Artifact Registry (guest_a)
# ──────────────────────────────────────────────

resource "google_artifact_registry_repository" "guest_a_dockerhub_mirror" {
  project       = google_project.guest_a.project_id
  location      = var.region
  repository_id = "dockerhub-mirror"
  description   = "Docker Hub remote mirror (guest_a)"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"

  remote_repository_config {
    description = "Docker Hub upstream"
    docker_repository {
      public_repository = "DOCKER_HUB"
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_artifact_registry_repository" "guest_a_pypi_mirror" {
  project       = google_project.guest_a.project_id
  location      = var.region
  repository_id = "pypi-mirror"
  description   = "PyPI remote mirror (guest_a)"
  format        = "PYTHON"
  mode          = "REMOTE_REPOSITORY"

  remote_repository_config {
    description = "PyPI upstream"
    python_repository {
      public_repository = "PYPI"
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_artifact_registry_repository" "guest_a_build_output" {
  project       = google_project.guest_a.project_id
  location      = var.region
  repository_id = "build-output-${local.suffix}"
  description   = "Standard Docker repo for build push verification (guest_a)"
  format        = "DOCKER"
  mode          = "STANDARD_REPOSITORY"

  depends_on = [google_project_service.enabled]
}

# AR の vpcsc_config (project x region 単位)
resource "google_artifact_registry_vpcsc_config" "guest_a" {
  provider     = google-beta
  project      = google_project.guest_a.project_id
  location     = var.region
  vpcsc_policy = "ALLOW"

  depends_on = [google_project_service.enabled]
}

# build SA -> dockerhub mirror reader
resource "google_artifact_registry_repository_iam_member" "guest_a_build_sa_reader_dockerhub" {
  project    = google_project.guest_a.project_id
  location   = google_artifact_registry_repository.guest_a_dockerhub_mirror.location
  repository = google_artifact_registry_repository.guest_a_dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build_guest_a.email}"
}

# build SA -> pypi mirror reader
resource "google_artifact_registry_repository_iam_member" "guest_a_build_sa_reader_pypi" {
  project    = google_project.guest_a.project_id
  location   = google_artifact_registry_repository.guest_a_pypi_mirror.location
  repository = google_artifact_registry_repository.guest_a_pypi_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build_guest_a.email}"
}

# build SA -> build-output writer
resource "google_artifact_registry_repository_iam_member" "guest_a_build_sa_writer_output" {
  project    = google_project.guest_a.project_id
  location   = google_artifact_registry_repository.guest_a_build_output.location
  repository = google_artifact_registry_repository.guest_a_build_output.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.build_guest_a.email}"
}

# P4SA (Cloud Build Service Agent) -> dockerhub mirror reader
# (build step.name = AR image を引いた時、Worker docker daemon は P4SA で pull する)
resource "google_artifact_registry_repository_iam_member" "guest_a_p4sa_reader_dockerhub" {
  project    = google_project.guest_a.project_id
  location   = google_artifact_registry_repository.guest_a_dockerhub_mirror.location
  repository = google_artifact_registry_repository.guest_a_dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${local.cb_agent_sa["guest_a"]}"
}

# ──────────────────────────────────────────────
# Cloud Build Worker Pool (guest_a, Shared VPC service project)
#
# peered_network は host (base) の main VPC を full self_link 形式で指定する。
# Shared VPC により、 base 側で既に張ってある google_service_networking_connection
# の peering を共有して使う。
# ──────────────────────────────────────────────

resource "google_cloudbuild_worker_pool" "guest_a" {
  name     = "private-pool-guest-a-${local.suffix}"
  project  = google_project.guest_a.project_id
  location = var.region

  worker_config {
    disk_size_gb   = 100
    machine_type   = "e2-standard-2"
    no_external_ip = true
  }

  network_config {
    peered_network          = "projects/${google_project.base.project_id}/global/networks/${google_compute_network.main.name}"
    peered_network_ip_range = "/29"
  }

  depends_on = [
    google_compute_shared_vpc_service_project.guest_a,
    google_service_networking_connection.main,
    google_project_service.enabled,
    google_compute_subnetwork_iam_member.shared_network_user,
  ]
}

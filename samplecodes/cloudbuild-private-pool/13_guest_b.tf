# ──────────────────────────────────────────────
# guest_b (Perimeter B 配下 / Shared VPC service project / Case 4)
#
# 中身は guest_a と同形だが、配置先プロジェクトと境界 (Perimeter B) が違う。
# host (base) は Perimeter A 側にいるので、 guest_b の pool は
# 「pool は Perimeter B、 peering ネットワークは Perimeter A の host」
# という境界跨ぎの構図 (= Case 4) を作る。
# ──────────────────────────────────────────────

resource "google_service_account" "build_guest_b" {
  project      = google_project.guest_b.project_id
  account_id   = "cb-build-sa-guest-b-${local.suffix}"
  display_name = "Cloud Build job SA (guest_b, VPC-SC verify)"

  depends_on = [google_project_service.enabled]
}

resource "google_service_account_iam_member" "guest_b_agent_act_as_build_sa" {
  service_account_id = google_service_account.build_guest_b.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${local.cb_agent_sa["guest_b"]}"
}

resource "google_project_iam_member" "guest_b_build_sa_log_writer" {
  project = google_project.guest_b.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.build_guest_b.email}"
}

# ──────────────────────────────────────────────
# Artifact Registry (guest_b)
# ──────────────────────────────────────────────

resource "google_artifact_registry_repository" "guest_b_dockerhub_mirror" {
  project       = google_project.guest_b.project_id
  location      = var.region
  repository_id = "dockerhub-mirror"
  description   = "Docker Hub remote mirror (guest_b)"
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

resource "google_artifact_registry_repository" "guest_b_pypi_mirror" {
  project       = google_project.guest_b.project_id
  location      = var.region
  repository_id = "pypi-mirror"
  description   = "PyPI remote mirror (guest_b)"
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

resource "google_artifact_registry_repository" "guest_b_build_output" {
  project       = google_project.guest_b.project_id
  location      = var.region
  repository_id = "build-output-${local.suffix}"
  description   = "Standard Docker repo for build push verification (guest_b)"
  format        = "DOCKER"
  mode          = "STANDARD_REPOSITORY"

  depends_on = [google_project_service.enabled]
}

resource "google_artifact_registry_vpcsc_config" "guest_b" {
  provider     = google-beta
  project      = google_project.guest_b.project_id
  location     = var.region
  vpcsc_policy = "ALLOW"

  depends_on = [google_project_service.enabled]
}

resource "google_artifact_registry_repository_iam_member" "guest_b_build_sa_reader_dockerhub" {
  project    = google_project.guest_b.project_id
  location   = google_artifact_registry_repository.guest_b_dockerhub_mirror.location
  repository = google_artifact_registry_repository.guest_b_dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build_guest_b.email}"
}

resource "google_artifact_registry_repository_iam_member" "guest_b_build_sa_reader_pypi" {
  project    = google_project.guest_b.project_id
  location   = google_artifact_registry_repository.guest_b_pypi_mirror.location
  repository = google_artifact_registry_repository.guest_b_pypi_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.build_guest_b.email}"
}

resource "google_artifact_registry_repository_iam_member" "guest_b_build_sa_writer_output" {
  project    = google_project.guest_b.project_id
  location   = google_artifact_registry_repository.guest_b_build_output.location
  repository = google_artifact_registry_repository.guest_b_build_output.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.build_guest_b.email}"
}

resource "google_artifact_registry_repository_iam_member" "guest_b_p4sa_reader_dockerhub" {
  project    = google_project.guest_b.project_id
  location   = google_artifact_registry_repository.guest_b_dockerhub_mirror.location
  repository = google_artifact_registry_repository.guest_b_dockerhub_mirror.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${local.cb_agent_sa["guest_b"]}"
}

# ──────────────────────────────────────────────
# Cloud Build Worker Pool (guest_b, Shared VPC service project)
# ──────────────────────────────────────────────

resource "google_cloudbuild_worker_pool" "guest_b" {
  name     = "private-pool-guest-b-${local.suffix}"
  project  = google_project.guest_b.project_id
  location = var.region

  worker_config {
    disk_size_gb   = 100
    machine_type   = "e2-standard-2"
    no_external_ip = true
  }

  network_config {
    peered_network          = "projects/${data.google_project.base.project_id}/global/networks/${google_compute_network.main.name}"
    peered_network_ip_range = "/29"
  }

  depends_on = [
    google_compute_shared_vpc_service_project.guest_b,
    google_service_networking_connection.main,
    google_project_service.enabled,
    google_compute_subnetwork_iam_member.shared_network_user,
  ]
}

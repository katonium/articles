# ──────────────────────────────────────────────
# Artifact Registry
#
# - DockerHub mirror (REMOTE_REPOSITORY mode = DOCKER_HUB)
#   検証ビルドでは asia-northeast1-docker.pkg.dev/PROJECT/dockerhub-mirror/library/python:3.12-slim
#   のように経由する
#
# - PyPI mirror (REMOTE_REPOSITORY mode = PYPI)
#   pip install --index-url https://REGION-python.pkg.dev/PROJECT/pypi-mirror/simple/
#
# - Docker push 先 (STANDARD_REPOSITORY mode)
#   ビルド成果物の検証用 push 先
# ──────────────────────────────────────────────

resource "google_artifact_registry_repository" "dockerhub_mirror" {
  project       = var.project_id
  location      = var.region
  repository_id = "dockerhub-mirror"
  description   = "Docker Hub remote mirror for VPC-SC verification"
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

resource "google_artifact_registry_repository" "pypi_mirror" {
  project       = var.project_id
  location      = var.region
  repository_id = "pypi-mirror"
  description   = "PyPI remote mirror for VPC-SC verification"
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

resource "google_artifact_registry_repository" "build_output" {
  project       = var.project_id
  location      = var.region
  repository_id = "build-output-${local.suffix}"
  description   = "Standard Docker repo for build push verification"
  format        = "DOCKER"
  mode          = "STANDARD_REPOSITORY"

  depends_on = [google_project_service.enabled]
}

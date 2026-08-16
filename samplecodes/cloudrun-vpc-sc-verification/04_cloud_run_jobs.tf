# ──────────────────────────────────────────────
# Cloud Run Jobs (google_cloud_run_v2_job)
#
# Job 5 つ:
#   - case0_base:     base project / base connector / base resource (Perim A 内、同一プロジェクト)
#   - case2_ga_host:  guest_a / guest_a connector / base resource (Perim A 内、cross-project)
#   - case3_ga_self:  guest_a / guest_a connector / guest_a resource (Perim A 内、同一プロジェクト)
#   - case4_gb_host:  guest_b / guest_b connector / base resource (Perim B → Perim A、cross-perim deny を期待)
#   - case5_gb_self:  guest_b / guest_b connector / guest_b resource (Perim B 内、Shared VPC は host=base なので cross-perim Shared VPC が成立しないことを期待)
#
# image は google-cloud-sdk:slim を使い、 command で直接 probe script を流し込む。
# 別 image ビルド/push 不要。
#
# vpc_access.egress = "ALL_TRAFFIC" にして、 Google API 行きも含めて connector を
# 経由させる。 これにより VPC firewall + DNS routing + VPC-SC 境界の効果を測定できる。
# ──────────────────────────────────────────────

locals {
  # 共通の probe スクリプト。 env vars TARGET_BUCKET, TARGET_PROJECT, TARGET_DATASET を読む。
  # bq CLI は default project の credential を取りに行くので CLOUDSDK_CORE_PROJECT を
  # Cloud Run Job が動作しているプロジェクトに設定しておく。
  probe_script = <<-EOT
    set -euo pipefail
    echo "TARGET_BUCKET=$${TARGET_BUCKET}"
    echo "TARGET_PROJECT=$${TARGET_PROJECT}"
    echo "TARGET_DATASET=$${TARGET_DATASET}"

    echo "[probe] gsutil ls gs://$${TARGET_BUCKET}/ ..."
    gsutil ls "gs://$${TARGET_BUCKET}/"

    echo "[probe] bq show $${TARGET_PROJECT}:$${TARGET_DATASET}.t ..."
    bq --project_id="$${TARGET_PROJECT}" show --format=prettyjson "$${TARGET_PROJECT}:$${TARGET_DATASET}.t"

    echo "[probe] ok"
  EOT

  job_image = "gcr.io/google.com/cloudsdktool/cloud-sdk:slim"
}

# ──────────────────────────────────────────────
# Case 0: base / base connector / base resource
# ──────────────────────────────────────────────
resource "google_cloud_run_v2_job" "case0_base" {
  name     = "case0-base-${var.suffix}"
  project  = data.google_project.base.project_id
  location = var.region

  template {
    template {
      service_account = google_service_account.job_base.email
      max_retries     = 0
      timeout         = "300s"

      vpc_access {
        connector = google_vpc_access_connector.base.id
        egress    = "ALL_TRAFFIC"
      }

      containers {
        image   = local.job_image
        command = ["bash", "-c", local.probe_script]

        env {
          name  = "TARGET_BUCKET"
          value = google_storage_bucket.base.name
        }
        env {
          name  = "TARGET_PROJECT"
          value = google_bigquery_dataset.base.project
        }
        env {
          name  = "TARGET_DATASET"
          value = google_bigquery_dataset.base.dataset_id
        }
        env {
          name  = "CLOUDSDK_CORE_PROJECT"
          value = data.google_project.base.project_id
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_bigquery_table.base,
  ]
}

# ──────────────────────────────────────────────
# Case 2: guest_a / guest_a connector / base resource (cross-project, same Perim A)
# ──────────────────────────────────────────────
resource "google_cloud_run_v2_job" "case2_ga_host" {
  name     = "case2-ga-host-${var.suffix}"
  project  = data.google_project.guest_a.project_id
  location = var.region

  template {
    template {
      service_account = google_service_account.job_guest_a.email
      max_retries     = 0
      timeout         = "300s"

      vpc_access {
        connector = google_vpc_access_connector.guest_a.id
        egress    = "ALL_TRAFFIC"
      }

      containers {
        image   = local.job_image
        command = ["bash", "-c", local.probe_script]

        env {
          name  = "TARGET_BUCKET"
          value = google_storage_bucket.base.name
        }
        env {
          name  = "TARGET_PROJECT"
          value = google_bigquery_dataset.base.project
        }
        env {
          name  = "TARGET_DATASET"
          value = google_bigquery_dataset.base.dataset_id
        }
        env {
          name  = "CLOUDSDK_CORE_PROJECT"
          value = data.google_project.guest_a.project_id
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_bigquery_table.base,
  ]
}

# ──────────────────────────────────────────────
# Case 3: guest_a / guest_a connector / guest_a resource (same Perim A, in-project)
# ──────────────────────────────────────────────
resource "google_cloud_run_v2_job" "case3_ga_self" {
  name     = "case3-ga-self-${var.suffix}"
  project  = data.google_project.guest_a.project_id
  location = var.region

  template {
    template {
      service_account = google_service_account.job_guest_a.email
      max_retries     = 0
      timeout         = "300s"

      vpc_access {
        connector = google_vpc_access_connector.guest_a.id
        egress    = "ALL_TRAFFIC"
      }

      containers {
        image   = local.job_image
        command = ["bash", "-c", local.probe_script]

        env {
          name  = "TARGET_BUCKET"
          value = google_storage_bucket.guest_a.name
        }
        env {
          name  = "TARGET_PROJECT"
          value = google_bigquery_dataset.guest_a.project
        }
        env {
          name  = "TARGET_DATASET"
          value = google_bigquery_dataset.guest_a.dataset_id
        }
        env {
          name  = "CLOUDSDK_CORE_PROJECT"
          value = data.google_project.guest_a.project_id
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_bigquery_table.guest_a,
  ]
}

# ──────────────────────────────────────────────
# Case 4: guest_b / guest_b connector / base resource (Perim B → Perim A, deny 期待)
# ──────────────────────────────────────────────
resource "google_cloud_run_v2_job" "case4_gb_host" {
  name     = "case4-gb-host-${var.suffix}"
  project  = data.google_project.guest_b.project_id
  location = var.region

  template {
    template {
      service_account = google_service_account.job_guest_b.email
      max_retries     = 0
      timeout         = "300s"

      vpc_access {
        connector = google_vpc_access_connector.guest_b.id
        egress    = "ALL_TRAFFIC"
      }

      containers {
        image   = local.job_image
        command = ["bash", "-c", local.probe_script]

        env {
          name  = "TARGET_BUCKET"
          value = google_storage_bucket.base.name
        }
        env {
          name  = "TARGET_PROJECT"
          value = google_bigquery_dataset.base.project
        }
        env {
          name  = "TARGET_DATASET"
          value = google_bigquery_dataset.base.dataset_id
        }
        env {
          name  = "CLOUDSDK_CORE_PROJECT"
          value = data.google_project.guest_b.project_id
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_bigquery_table.base,
  ]
}

# ──────────────────────────────────────────────
# Case 5: guest_b / guest_b connector / guest_b resource (Perim B 内、cross-perim Shared VPC 自体は不可)
# ──────────────────────────────────────────────
resource "google_cloud_run_v2_job" "case5_gb_self" {
  name     = "case5-gb-self-${var.suffix}"
  project  = data.google_project.guest_b.project_id
  location = var.region

  template {
    template {
      service_account = google_service_account.job_guest_b.email
      max_retries     = 0
      timeout         = "300s"

      vpc_access {
        connector = google_vpc_access_connector.guest_b.id
        egress    = "ALL_TRAFFIC"
      }

      containers {
        image   = local.job_image
        command = ["bash", "-c", local.probe_script]

        env {
          name  = "TARGET_BUCKET"
          value = google_storage_bucket.guest_b.name
        }
        env {
          name  = "TARGET_PROJECT"
          value = google_bigquery_dataset.guest_b.project
        }
        env {
          name  = "TARGET_DATASET"
          value = google_bigquery_dataset.guest_b.dataset_id
        }
        env {
          name  = "CLOUDSDK_CORE_PROJECT"
          value = data.google_project.guest_b.project_id
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
    google_bigquery_table.guest_b,
  ]
}

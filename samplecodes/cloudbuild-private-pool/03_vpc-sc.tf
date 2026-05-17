# ──────────────────────────────────────────────
# Access Context Manager Access Policy (folder-scoped)
#
# 組織を汚さないため、parent / scopes をフォルダに限定する。
# 同一フォルダに既存ポリシーがある場合は作成失敗するため、
# その場合は data source 切替か事前削除を行うこと。
# ──────────────────────────────────────────────

resource "google_access_context_manager_access_policy" "folder" {
  parent = "organizations/${var.org_id}"
  title  = "vpc-sc-cb-private-pool-verify-${local.suffix}"
  scopes = ["folders/${var.folder_id}"]
}

# ──────────────────────────────────────────────
# Service Perimeter
#
# resources にフォルダ配下の検証対象プロジェクトを入れる。
# 「フォルダ全プロジェクトを内部に入れる」要件は、本フォルダ配下に
# 検証対象プロジェクト 1 つしか作らない前提でこれを満たす。
# 別プロジェクトをあとから追加する場合はここに足す。
# ──────────────────────────────────────────────

resource "google_access_context_manager_service_perimeter" "main" {
  parent = "accessPolicies/${google_access_context_manager_access_policy.folder.name}"
  name   = "accessPolicies/${google_access_context_manager_access_policy.folder.name}/servicePerimeters/cb_pp_${local.suffix}"
  title  = "cb-pp-${local.suffix}"

  # ENFORCED モード。dry-run で先に確認したい場合は use_explicit_dry_run_spec=true + spec block へ。
  perimeter_type = "PERIMETER_TYPE_REGULAR"

  status {
    # Perimeter A: base (Cloud Build mainline + Shared VPC host を兼ねる) + guest_a。
    # guest_b だけ Perimeter B 側に置く (境界分離検証 Case 4)。
    resources = [
      "projects/${data.google_project.base.number}",
      "projects/${google_project.guest_a.number}",
    ]
    restricted_services = var.perimeter_restricted_services

    vpc_accessible_services {
      enable_restriction = true
      allowed_services   = var.perimeter_restricted_services
    }

    # var.ingress_identities (terraform.tfvars で外注) を Ingress 許可。
    # 「access_level = *」「service_name = *」で any に倒す（検証目的）。
    ingress_policies {
      ingress_from {
        identities = var.ingress_identities

        sources {
          access_level = "*"
        }
      }

      ingress_to {
        resources = ["*"]

        operations {
          service_name = "*"
        }
      }
    }
  }

  depends_on = [
    google_project_service.enabled,
  ]
}

# ──────────────────────────────────────────────
# Perimeter B: Case 4 (境界分離) 専用。 guest-b のみ resources に持つ。
# host-b は Perimeter A 側に居るので、 pool が居る guest-b 側だけ
# Perimeter B で保護される構図になる。
# ──────────────────────────────────────────────

resource "google_access_context_manager_service_perimeter" "secondary" {
  parent = "accessPolicies/${google_access_context_manager_access_policy.folder.name}"
  name   = "accessPolicies/${google_access_context_manager_access_policy.folder.name}/servicePerimeters/cb_pp_b_${local.suffix}"
  title  = "cb-pp-b-${local.suffix}"

  perimeter_type = "PERIMETER_TYPE_REGULAR"

  status {
    resources           = ["projects/${google_project.guest_b.number}"]
    restricted_services = var.perimeter_restricted_services

    vpc_accessible_services {
      enable_restriction = true
      allowed_services   = var.perimeter_restricted_services
    }

    ingress_policies {
      ingress_from {
        identities = var.ingress_identities

        sources {
          access_level = "*"
        }
      }

      ingress_to {
        resources = ["*"]

        operations {
          service_name = "*"
        }
      }
    }
  }

  depends_on = [google_project_service.enabled]
}

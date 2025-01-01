module "oidc" {
  source  = "terraform-google-modules/github-actions-runners/google//modules/gh-oidc"
  version = "~> 4.0"

  project_id          = var.project_id
  pool_id             = "sample-github-actions-pool"
  provider_id         = "sample-github-actions-provider"
  attribute_condition = "assertion.repository_owner=='xxxxxx'"
  issuer_uri          = "https://token.actions.githubusercontent.com"
  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.actor"      = "assertion.actor"
    "attribute.repository" = "assertion.repository"
  }
  sa_mapping = {
    (google_service_account.github_actions_sa.account_id) = {
      sa_name   = google_service_account.github_actions_sa.name
      attribute = "attribute.repository/xxxxxx/xxxxxx"
    }
  }
}

# サービスアカウントを作成
resource "google_service_account" "github_actions_sa" {
  account_id   = "github-actions-sa"
  display_name = "GitHub Actions Service Account"
}

# サービスアカウントに必要なロールを追加
# 今回は最低限のロールのみ追加しています。変更するリソースに応じて適切なロールを追加してください。
resource "google_project_iam_member" "github_actions_sa_serviceAccountUser" {
  project = var.project_id
  role    = "roles/iam.serviceAccountUser"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_storageObjectAdmin" {
  project = var.project_id
  role    = "roles/storage.objectAdmin"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_viewer" {
  project = var.project_id
  role    = "roles/viewer"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}
resource "google_project_iam_member" "github_actions_sa_workloadIdentityUser" {
  project = var.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = "serviceAccount:${google_service_account.github_actions_sa.email}"
}

resource "google_project_service" "iamcredentials_api" {
  project = var.project_id
  service = "iamcredentials.googleapis.com"
}
resource "google_project_service" "iam_api" {
  project = var.project_id
  service = "iam.googleapis.com"
}
resource "google_project_service" "cloudresourcemanager_api" {
  project = var.project_id
  service = "cloudresourcemanager.googleapis.com"
}


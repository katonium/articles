terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.45"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 5.45"
    }
  }
}

# このモジュールは cloudbuild-private-pool で構築済みの base / guest_a / guest_b
# プロジェクトと Shared VPC を再利用する。新規プロジェクトや Perimeter は作らない。
# project / billing は cloudbuild 側と同じものを指定する想定。

provider "google" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
  request_reason        = "vpc-sc-cloudrun-vpc-connector-verification"
}

provider "google-beta" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
  request_reason        = "vpc-sc-cloudrun-vpc-connector-verification"
}

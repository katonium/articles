terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.45"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "google" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
  request_reason        = "vpc-sc-cloudbuild-private-pool-verification"
}

data "google_project" "main" {
  project_id = var.project_id
}

resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  suffix      = random_id.suffix.hex
  cb_agent_sa = "service-${data.google_project.main.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
}

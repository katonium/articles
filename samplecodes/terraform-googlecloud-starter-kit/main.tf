variable "project_id" {
  type        = string
  description = "Google Cloud Project ID"
  default     = "xxxxxxxxxxxxxxxxxx"
}

provider "google" {
  project = var.project_id
  region  = "asia-northeast1"
}

resource "random_id" "default" {
  byte_length = 2
}

resource "google_storage_bucket" "default" {
  name     = "terraform-remote-backend-${random_id.default.hex}"
  location = "ASIA-NORTHEAST1"

  force_destroy               = false
  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true

  versioning {
    enabled = true
  }
  # # prevent from accidental terraform destroy
  # lifecycle {
  #   prevent_destroy = true
  # }
}

resource "local_file" "default" {
  file_permission = "0644"
  filename        = "${path.module}/backend.tf"

  # You can store the template in a file and use the templatefile function for
  # more modularity, if you prefer, instead of storing the template inline as
  # we do here.
  content = <<-EOT
  terraform {
    backend "gcs" {
      bucket = "${google_storage_bucket.default.name}"
    }
  }
  EOT
}
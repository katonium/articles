terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.16"
    }
  }

  required_version = ">= 1.2.0"
}

provider "aws" {
  region = "ap-northeast-1"
}

resource "aws_instance" "app_server" {
  ami           = "ami-0037237888be2fe22"
  instance_type = "t2.micro"

  tags = {
    Name = "ExampleAppServerInstance"
  }
}

resource "aws_instance" "database_server" {
  ami           = "ami-0037237888be2fe22"
  instance_type = "t2.micro"


  tags = {
    Name = "ExampleAppServerInstance"
  }
}

.PHONY: init plan apply test report destroy clean check-tfvars

check-tfvars:
	@test -f terraform.tfvars || (echo "terraform.tfvars not found. Copy terraform.tfvars.example and edit." && exit 1)

init:
	terraform init

plan: check-tfvars
	terraform plan

apply: check-tfvars
	terraform apply -auto-approve

test: check-tfvars
	go test -v -timeout 1800s ./...

report: check-tfvars
	go test -v -timeout 1800s -json ./... | \
		go-test-report -o test-report.html -t "VPC-SC Cloud Build Private Pool 仕様検証レポート"

destroy: check-tfvars
	terraform destroy -auto-approve

clean:
	rm -rf .terraform terraform.tfstate terraform.tfstate.backup test-report.html

// https://atlasgo.io/atlas-schema/projects
env "local" {
  // Declare where the schema definition resides.
  // Also supported: ["file://multi.hcl", "file://schema.hcl"].
  // src = ["file://schema_users_and_blogposts.hcl"]
  src = ["file://schema_users_and_blogposts.hcl", "file://schema_authors.hcl"]

  // Define the URL of the database which is managed
  // in this environment.
  url = "mysql://root:pass@localhost:3306/example"

  // Define the URL of the Dev Database for this environment
  // See: https://atlasgo.io/concepts/dev-database
  dev = "docker://mysql/8/dev"
}
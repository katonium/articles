table "authors" {
  schema = schema.example
  column "id" {
    null = false
    type = int
  }
  column "name" {
    null = false
    type = varchar(100)
  }
  column "age" {
    null = true
    type = int
  }
  primary_key {
    columns = [column.id]
  }
}

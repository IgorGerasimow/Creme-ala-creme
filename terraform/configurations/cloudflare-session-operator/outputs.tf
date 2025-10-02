output "s3_bucket_name" {
  description = "S3 bucket name"
  value       = module.s3_bucket.s3_bucket_id
}

output "s3_bucket_arn" {
  description = "S3 bucket ARN"
  value       = module.s3_bucket.s3_bucket_arn
}

output "db_endpoint" {
  description = "RDS endpoint (hostname:port)"
  value       = module.db.db_instance_endpoint
}

output "db_address" {
  description = "RDS hostname"
  value       = module.db.db_instance_address
}

output "db_port" {
  description = "RDS port"
  value       = module.db.db_instance_port
}

output "db_name" {
  description = "Database name"
  value       = var.db_name
}

output "db_username" {
  description = "Database user"
  value       = var.db_username
}

output "kafka_topic_id" {
  description = "ID topic name"
  value       = kafka_topic.id.name
}

output "kafka_topic_sessions" {
  description = "Sessions topic name"
  value       = kafka_topic.sessions.name
}



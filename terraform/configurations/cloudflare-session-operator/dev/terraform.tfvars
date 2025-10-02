aws_region = "us-east-1"

private_subnet_ids = [
  "subnet-aaaaaaaa",
  "subnet-bbbbbbbb"
]

db_instance_class         = "db.t4g.micro"
db_allocated_storage      = 20
db_max_allocated_storage  = 100
db_engine_version         = "14.10"
db_name                   = "sessions"
db_username               = "app"
db_multi_az               = false
db_backup_retention       = 1
db_deletion_protection    = false
db_skip_final_snapshot    = true
db_performance_insights_enabled = false

kafka_bootstrap_servers = [
  "localhost:9092"
]
kafka_tls_enabled       = false
kafka_skip_tls_verify   = false
kafka_default_partitions = 3
kafka_default_replication_factor = 1

kafka_topic_id_name       = "id"
kafka_topic_sessions_name = "sessions"

kafka_default_topic_config = {
  "cleanup.policy"      = "delete"
  "retention.ms"        = "604800000"
  "min.insync.replicas" = "1"
}

kafka_sessions_topic_overrides = {
  "cleanup.policy" = "compact,delete"
}



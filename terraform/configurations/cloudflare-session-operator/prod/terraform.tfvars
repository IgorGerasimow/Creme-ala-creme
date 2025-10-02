aws_region = "us-east-1"

private_subnet_ids = [
  "subnet-aaaaaaaa",
  "subnet-bbbbbbbb"
]

db_instance_class         = "db.m6g.large"
db_allocated_storage      = 100
db_max_allocated_storage  = 1000
db_engine_version         = "14.10"
db_name                   = "sessions"
db_username               = "app"
db_multi_az               = true
db_backup_retention       = 14
db_deletion_protection    = true
db_skip_final_snapshot    = false
db_performance_insights_enabled = true

kafka_bootstrap_servers = [
  "b-1.msk-prod.example:9094",
  "b-2.msk-prod.example:9094",
  "b-3.msk-prod.example:9094"
]
kafka_tls_enabled       = true
kafka_skip_tls_verify   = false
kafka_default_partitions = 12
kafka_default_replication_factor = 3

kafka_topic_id_name       = "id"
kafka_topic_sessions_name = "sessions"

kafka_default_topic_config = {
  "cleanup.policy"      = "delete"
  "retention.ms"        = "2592000000" # 30 days
  "min.insync.replicas" = "2"
}

kafka_sessions_topic_overrides = {
  "cleanup.policy" = "compact,delete"
}



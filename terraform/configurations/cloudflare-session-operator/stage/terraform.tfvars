aws_region = "us-east-1"

private_subnet_ids = [
  "subnet-aaaaaaaa",
  "subnet-bbbbbbbb"
]

db_instance_class         = "db.t4g.small"
db_allocated_storage      = 50
db_max_allocated_storage  = 200
db_engine_version         = "14.10"
db_name                   = "sessions"
db_username               = "app"
db_multi_az               = true
db_backup_retention       = 7
db_deletion_protection    = true
db_skip_final_snapshot    = false
db_performance_insights_enabled = true

kafka_bootstrap_servers = [
  "b-1.msk-stage.example:9094",
  "b-2.msk-stage.example:9094"
]
kafka_tls_enabled       = true
kafka_skip_tls_verify   = false
kafka_default_partitions = 6
kafka_default_replication_factor = 3

kafka_topic_id_name       = "id"
kafka_topic_sessions_name = "sessions"

kafka_default_topic_config = {
  "cleanup.policy"      = "delete"
  "retention.ms"        = "1209600000" # 14 days
  "min.insync.replicas" = "2"
}

kafka_sessions_topic_overrides = {
  "cleanup.policy" = "compact,delete"
}



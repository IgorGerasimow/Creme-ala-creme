variable "app_name" {
  description = "Application name used for tagging and resource naming"
  type        = string
  default     = "cloudflare-session-operator"
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs for RDS"
  type        = list(string)
}

variable "db_security_group_ids" {
  description = "Security group IDs to attach to the RDS instance"
  type        = list(string)
  default     = []
}

variable "db_engine_version" {
  description = "PostgreSQL engine version, e.g., 14.10"
  type        = string
  default     = "14.10"
}

variable "db_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.micro"
}

variable "db_allocated_storage" {
  description = "Initial allocated storage (GiB)"
  type        = number
  default     = 20
}

variable "db_max_allocated_storage" {
  description = "Maximum allocated storage for autoscaling (GiB)"
  type        = number
  default     = 100
}

variable "db_name" {
  description = "Database name"
  type        = string
  default     = "sessions"
}

variable "db_username" {
  description = "Master username"
  type        = string
  default     = "app"
}

variable "db_master_password" {
  description = "Master password (if not provided, a random password will be generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "db_multi_az" {
  description = "Whether to create a Multi-AZ RDS deployment"
  type        = bool
  default     = false
}

variable "db_backup_retention" {
  description = "Backup retention period in days"
  type        = number
  default     = 7
}

variable "db_deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = false
}

variable "db_skip_final_snapshot" {
  description = "Skip final snapshot on destroy"
  type        = bool
  default     = true
}

variable "db_performance_insights_enabled" {
  description = "Enable Performance Insights"
  type        = bool
  default     = false
}

variable "kafka_bootstrap_servers" {
  description = "List of Kafka bootstrap servers (host:port)"
  type        = list(string)
}

variable "kafka_tls_enabled" {
  description = "Enable TLS for Kafka client"
  type        = bool
  default     = true
}

variable "kafka_skip_tls_verify" {
  description = "Skip TLS verification"
  type        = bool
  default     = false
}

variable "kafka_ca_cert" {
  description = "PEM-encoded CA certificate for Kafka TLS"
  type        = string
  default     = null
}

variable "kafka_client_cert" {
  description = "PEM-encoded client certificate for mTLS"
  type        = string
  default     = null
}

variable "kafka_client_key" {
  description = "PEM-encoded client key for mTLS"
  type        = string
  default     = null
  sensitive   = true
}

variable "kafka_sasl_username" {
  description = "SASL username for Kafka"
  type        = string
  default     = null
}

variable "kafka_sasl_password" {
  description = "SASL password for Kafka"
  type        = string
  default     = null
  sensitive   = true
}

variable "kafka_sasl_mechanism" {
  description = "SASL mechanism: PLAIN, SCRAM-SHA-256, SCRAM-SHA-512, etc."
  type        = string
  default     = null
}

variable "kafka_topic_id_name" {
  description = "Name of the ID topic"
  type        = string
  default     = "id"
}

variable "kafka_topic_sessions_name" {
  description = "Name of the sessions topic"
  type        = string
  default     = "sessions"
}

variable "kafka_default_partitions" {
  description = "Default number of partitions for topics"
  type        = number
  default     = 3
}

variable "kafka_default_replication_factor" {
  description = "Default replication factor for topics"
  type        = number
  default     = 3
}

variable "kafka_default_topic_config" {
  description = "Default topic-level configuration applied to all topics"
  type        = map(string)
  default = {
    "cleanup.policy"        = "delete"
    "retention.ms"          = "604800000" # 7 days
    "min.insync.replicas"   = "2"
    "segment.ms"            = "3600000"   # 1 hour
    "retention.bytes"       = "-1"
  }
}

variable "kafka_id_topic_overrides" {
  description = "Overrides for the ID topic configuration"
  type        = map(string)
  default     = {}
}

variable "kafka_sessions_topic_overrides" {
  description = "Overrides for the sessions topic configuration (stream config)"
  type        = map(string)
  default = {
    "cleanup.policy" = "compact,delete"
  }
}



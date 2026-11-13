-- 题目 09 数据库初始化脚本（MySQL 8，utf8mb4）
-- 由本地 MySQL 实例导出（mysqldump --no-data），与 internal/fleetlog/model 的 AutoMigrate 结果一致。
-- 用法：mysql --host=127.0.0.1 --user=root --password=root < db/schema.sql
-- 说明：服务启动时仍会执行 AutoMigrate（幂等）；本文件用于「数据库初始化/变更文件」交付与人工建库。
--       预置账号与 5 台车由 repo.Seed() 在首次启动写入，不在此文件重复插入。

CREATE DATABASE IF NOT EXISTS `questions` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
USE `questions`;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `account` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `username` varchar(64) NOT NULL,
  `display_name` varchar(64) NOT NULL,
  `role` varchar(32) NOT NULL,
  `token` varchar(128) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_account_username` (`username`),
  UNIQUE KEY `idx_account_token` (`token`)
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `vehicle` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `code` varchar(32) NOT NULL,
  `name` varchar(64) NOT NULL,
  `token` varchar(128) NOT NULL,
  `initial_interval` bigint NOT NULL,
  `applied_interval` bigint NOT NULL,
  `applied_version` bigint NOT NULL DEFAULT '0',
  `latest_version` bigint NOT NULL DEFAULT '0',
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_vehicle_code` (`code`),
  UNIQUE KEY `idx_vehicle_token` (`token`)
) ENGINE=InnoDB AUTO_INCREMENT=6 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `application` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `applicant_id` bigint unsigned NOT NULL,
  `approver_id` bigint unsigned NOT NULL DEFAULT '0',
  `reason` varchar(512) NOT NULL,
  `interval` bigint NOT NULL,
  `status` varchar(32) NOT NULL,
  `version_no` bigint NOT NULL DEFAULT '1',
  `reject_reason` varchar(512) DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_application_applicant_id` (`applicant_id`),
  KEY `idx_application_status` (`status`)
) ENGINE=InnoDB AUTO_INCREMENT=2 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `application_version` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `application_id` bigint unsigned NOT NULL,
  `version_no` bigint NOT NULL,
  `reason` varchar(512) NOT NULL,
  `interval` bigint NOT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_application_version_application_id` (`application_id`)
) ENGINE=InnoDB AUTO_INCREMENT=2 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `campaign` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `application_id` bigint unsigned NOT NULL,
  `status` varchar(32) NOT NULL,
  `pilot_vehicles` varchar(128) NOT NULL,
  `fleet_key` varchar(32) NOT NULL,
  `pilot_started_at` datetime(3) DEFAULT NULL,
  `stopped_at` datetime(3) DEFAULT NULL,
  `stop_reason` varchar(512) DEFAULT NULL,
  `completed_at` datetime(3) DEFAULT NULL,
  `restore_started_at` datetime(3) DEFAULT NULL,
  `restored_completed` datetime(3) DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_campaign_application_id` (`application_id`),
  KEY `idx_campaign_status` (`status`),
  KEY `idx_campaign_fleet_key` (`fleet_key`)
) ENGINE=InnoDB AUTO_INCREMENT=2 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `vehicle_release` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `campaign_id` bigint unsigned NOT NULL,
  `vehicle_id` bigint unsigned NOT NULL,
  `version_no` bigint NOT NULL,
  `kind` varchar(16) NOT NULL,
  `target_interval` bigint NOT NULL,
  `prior_interval` bigint NOT NULL,
  `status` varchar(16) NOT NULL,
  `dispatched_at` datetime(3) DEFAULT NULL,
  `deadline_at` datetime(3) DEFAULT NULL,
  `receipt_at` datetime(3) DEFAULT NULL,
  `result_note` varchar(512) DEFAULT NULL,
  `late_success_at` datetime(3) DEFAULT NULL,
  `superseded` tinyint(1) NOT NULL DEFAULT '0',
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_vehicle_release_campaign_id` (`campaign_id`),
  KEY `idx_vehicle_version` (`vehicle_id`,`version_no`),
  KEY `idx_vehicle_release_status` (`status`)
) ENGINE=InnoDB AUTO_INCREMENT=6 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `receipt` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `release_id` bigint unsigned NOT NULL,
  `vehicle_id` bigint unsigned NOT NULL,
  `version_no` bigint NOT NULL,
  `success` tinyint(1) NOT NULL,
  `reason` varchar(512) DEFAULT NULL,
  `request_key` varchar(64) DEFAULT NULL,
  `duplicate` tinyint(1) NOT NULL DEFAULT '0',
  `ignored` tinyint(1) NOT NULL DEFAULT '0',
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_receipt_release_id` (`release_id`),
  KEY `idx_receipt_vehicle_id` (`vehicle_id`),
  KEY `idx_receipt_request_key` (`request_key`)
) ENGINE=InnoDB AUTO_INCREMENT=6 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `exception_record` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `kind` varchar(32) NOT NULL,
  `vehicle_id` bigint unsigned NOT NULL DEFAULT '0',
  `campaign_id` bigint unsigned NOT NULL DEFAULT '0',
  `detail` varchar(512) NOT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_exception_record_kind` (`kind`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE IF NOT EXISTS `audit_log` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `actor` varchar(64) NOT NULL,
  `role` varchar(32) NOT NULL,
  `action` varchar(64) NOT NULL,
  `target` varchar(64) NOT NULL,
  `detail` varchar(512) DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=11 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;


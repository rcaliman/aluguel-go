-- Fixture exclusiva dos testes; a aplicação não executa este arquivo.
CREATE TABLE tb_tenants (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`name` VARCHAR(255) NOT NULL,
`document` VARCHAR(255) NULL,
`cpf_cnpj` VARCHAR(255) NULL UNIQUE,
`date_of_birth` DATE NULL,
`address` VARCHAR(255) NULL,
`location` VARCHAR(255) NULL,
`state` VARCHAR(255) NULL,
`city` VARCHAR(255) NULL,
`marital_status` ENUM('SOLTEIRO','CASADO','DIVORCIADO','VIUVO','UNIAO_ESTAVEL') NULL,
`nationality` VARCHAR(255) NULL,
created_at DATETIME(6) NOT NULL,
updated_at DATETIME(6) NULL
) ENGINE=InnoDB;
CREATE TABLE tb_landlords (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`name` VARCHAR(255) NOT NULL,
`document` VARCHAR(255) NULL,
`cpf_cnpj` VARCHAR(255) NULL UNIQUE,
`date_of_birth` DATE NULL,
`address` VARCHAR(255) NULL,
`location` VARCHAR(255) NULL,
`state` VARCHAR(255) NULL,
`city` VARCHAR(255) NULL,
`marital_status` ENUM('SOLTEIRO','CASADO','DIVORCIADO','VIUVO','UNIAO_ESTAVEL') NULL,
`nationality` VARCHAR(255) NULL,
`main` BIT NULL,
created_at DATETIME(6) NOT NULL,
updated_at DATETIME(6) NULL
) ENGINE=InnoDB;
CREATE TABLE tb_properties (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`property_type` ENUM('LOJA','SALA_COMERCIAL','QUITINETE','APARTAMENTO','CONDOMINIO') NOT NULL,
`use_type` ENUM('RESIDENCIAL','COMERCIAL') NULL,
`address` VARCHAR(255) NULL,
`location` VARCHAR(255) NULL,
`state` VARCHAR(255) NULL,
`city` VARCHAR(255) NULL,
`number` VARCHAR(255) NULL,
`complement` VARCHAR(255) NULL,
`tenant_id` BIGINT NULL,
`rental_value` DOUBLE NULL,
`observation` VARCHAR(255) NULL,
`payment_day` VARCHAR(255) NULL,
created_at DATETIME(6) NOT NULL,
updated_at DATETIME(6) NULL,
FOREIGN KEY (tenant_id) REFERENCES tb_tenants(id) ON DELETE SET NULL
) ENGINE=InnoDB;
CREATE TABLE tb_users (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`username` VARCHAR(255) NOT NULL UNIQUE,
`password` VARCHAR(255) NOT NULL,
`role` ENUM('ADMIN','OPERADOR','LEITOR') NOT NULL,
created_at DATETIME(6) NOT NULL,
updated_at DATETIME(6) NULL
) ENGINE=InnoDB;
CREATE TABLE tb_energy (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`counter_1` DOUBLE NOT NULL,
`counter_2` DOUBLE NOT NULL,
`counter_3` DOUBLE NOT NULL,
`amount_1` DOUBLE NULL,
`amount_2` DOUBLE NULL,
`amount_3` DOUBLE NULL,
`kwh_value` DOUBLE NULL,
`bill_amount` DOUBLE NULL,
`date` DATE NULL
) ENGINE=InnoDB;
CREATE TABLE tb_commercial_energy (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`amount_1` DOUBLE NULL,
`amount_2` DOUBLE NULL,
`internal_counter` DOUBLE NULL,
`account_value` DOUBLE NULL,
`account_consumption` DOUBLE NULL,
`calculated_consumption_1` DOUBLE NULL,
`calculated_consumption_2` DOUBLE NULL,
`date` DATE NULL
) ENGINE=InnoDB;
CREATE TABLE tb_energy_title (
`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
`title_amount_1` VARCHAR(255) NULL,
`title_amount_2` VARCHAR(255) NULL,
`title_amount_3` VARCHAR(255) NULL,
`title_amount_4` VARCHAR(255) NULL,
`title_amount_5` VARCHAR(255) NULL
) ENGINE=InnoDB;
CREATE TABLE tb_contacts (id BIGINT AUTO_INCREMENT PRIMARY KEY,contact_type ENUM('CELULAR','EMAIL','TELEFONE') NOT NULL,contact VARCHAR(255) NOT NULL,tenant_id BIGINT NULL,landlord_id BIGINT NULL,created_at DATETIME(6) NOT NULL,updated_at DATETIME(6) NULL,FOREIGN KEY (tenant_id) REFERENCES tb_tenants(id),FOREIGN KEY (landlord_id) REFERENCES tb_landlords(id)) ENGINE=InnoDB;
CREATE TABLE tb_properties_logs (id BIGINT AUTO_INCREMENT PRIMARY KEY,property_id BIGINT,property_type ENUM('LOJA','SALA_COMERCIAL','QUITINETE','APARTAMENTO','CONDOMINIO'),number VARCHAR(255),address VARCHAR(255),rental_value DOUBLE,complement VARCHAR(255),observation VARCHAR(255),payment_day VARCHAR(255),tenant_name VARCHAR(255),tenant_cpf_cnpj VARCHAR(255),created_at DATETIME(6) NOT NULL,FOREIGN KEY (property_id) REFERENCES tb_properties(id)) ENGINE=InnoDB;

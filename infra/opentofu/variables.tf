variable "project_name" {
  type        = string
  description = "Name prefix for DO resources"
  default     = "nightloch-lite"
}

variable "region" {
  type        = string
  description = "DigitalOcean region"
  default     = "nyc3"
}

variable "size" {
  type        = string
  description = "Droplet size slug"
  default     = "s-1vcpu-2gb"
}

variable "image" {
  type        = string
  description = "Droplet image"
  default     = "ubuntu-24-04-x64"
}

variable "ssh_public_key_path" {
  type        = string
  description = "Path to SSH public key for droplet access"
}

variable "ssh_source_cidrs" {
  type        = list(string)
  description = "CIDRs allowed to SSH (22). Prefer your IP/32 for dogfood."
  default     = ["0.0.0.0/0", "::/0"]
}

variable "admin_user" {
  type        = string
  description = "Non-root deploy/admin user created by cloud-init"
  default     = "nightloch"
}

variable "cloudflare_zone_id" {
  type        = string
  description = "Cloudflare zone ID for nightloch.app"
}

variable "dns_ttl" {
  type        = number
  description = "TTL for unproxied A records (60 allowed when not proxied)"
  default     = 60
}

variable "enable_dns_records" {
  type        = bool
  description = "Create lite/agh/vpn A records (set false to only create droplet)"
  default     = true
}

variable "manage_resend_dns" {
  type        = bool
  description = "Create Resend verification records (set values from Resend dashboard)"
  default     = false
}

variable "resend_domain_verify_txt" {
  type        = string
  description = "Resend domain verification TXT value (host often resend._domainkey or as shown in Resend)"
  default     = ""
}

variable "resend_dkim_name" {
  type        = string
  description = "Resend DKIM record name (e.g. resend._domainkey)"
  default     = "resend._domainkey"
}

variable "resend_dkim_value" {
  type        = string
  description = "Resend DKIM TXT value"
  default     = ""
}

variable "resend_spf_include" {
  type        = string
  description = "SPF include for Resend (default include:resend.com)"
  default     = "v=spf1 include:resend.com ~all"
}

variable "enable_dmarc" {
  type        = bool
  description = "Create a basic DMARC record at _dmarc.nightloch.app"
  default     = true
}

variable "dmarc_rua" {
  type        = string
  description = "DMARC aggregate report mailto (e.g. mailto:you@example.com)"
  default     = ""
}

variable "staging_host" {
  type        = string
  description = "Primary staging hostname (Path A)"
  default     = "lite.nightloch.app"
}

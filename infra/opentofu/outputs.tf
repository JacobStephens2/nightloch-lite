output "droplet_id" {
  description = "DigitalOcean droplet ID"
  value       = digitalocean_droplet.nightloch.id
}

output "droplet_ipv4" {
  description = "Public IPv4 of the nightloch droplet"
  value       = digitalocean_droplet.nightloch.ipv4_address
}

output "ssh_example" {
  description = "SSH command (after cloud-init finishes, ~1–3 minutes)"
  value       = "ssh ${var.admin_user}@${digitalocean_droplet.nightloch.ipv4_address}"
}

output "staging_url" {
  description = "Staging HTTPS URL (after DNS + cert)"
  value       = "https://${var.staging_host}"
}

output "console_password" {
  description = "Initial console/SSH password for admin_user — store in password manager, change after first login"
  value       = random_password.console.result
  sensitive   = true
}

output "dns_records" {
  description = "Unproxied hostnames pointing at the droplet"
  value = var.enable_dns_records ? {
    lite = "lite.nightloch.app -> ${digitalocean_droplet.nightloch.ipv4_address}"
    agh  = "agh.nightloch.app -> ${digitalocean_droplet.nightloch.ipv4_address}"
    vpn  = "vpn.nightloch.app -> ${digitalocean_droplet.nightloch.ipv4_address}"
  } : {}
}

output "next_steps" {
  value = <<-EOT
    1. Store console_password in your password manager (tofu output -raw console_password).
    2. Wait for cloud-init: ssh and check /var/log/nightloch-firstboot.log
    3. Open https://${var.staging_host} — expect "Origin OK — Slice 1 staging."
    4. dig +short vpn.nightloch.app  # should match droplet_ipv4, grey-cloud
    5. Do NOT enable NAT yet — see docs/ops/NAT-CHECKLIST.md
  EOT
}

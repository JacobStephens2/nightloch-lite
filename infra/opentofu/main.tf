locals {
  tags = ["nightloch", "nightloch-lite", var.project_name]

  cloud_init = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    admin_user       = var.admin_user
    console_password = random_password.console.result
    staging_host     = var.staging_host
  })
}

resource "random_password" "console" {
  length  = 24
  special = false
}

resource "digitalocean_ssh_key" "admin" {
  name       = "${var.project_name}-admin"
  public_key = file(pathexpand(var.ssh_public_key_path))
}

resource "digitalocean_droplet" "nightloch" {
  name   = var.project_name
  region = var.region
  size   = var.size
  image  = var.image

  ssh_keys = [digitalocean_ssh_key.admin.fingerprint]
  tags     = local.tags

  monitoring        = true
  backups           = true
  ipv6              = false
  droplet_agent     = true
  graceful_shutdown = true
  user_data         = local.cloud_init

  # Explicit daily policy (F24) — do not rely on provider default alone.
  backup_policy {
    plan = "daily"
    hour = 8
  }

  lifecycle {
    # cloud-init is first-boot only; changing the template must not replace the VM.
    ignore_changes = [user_data]
  }
}

resource "digitalocean_firewall" "nightloch" {
  name        = "${var.project_name}-fw"
  droplet_ids = [digitalocean_droplet.nightloch.id]
  tags        = local.tags

  inbound_rule {
    protocol         = "tcp"
    port_range       = "22"
    source_addresses = var.ssh_source_cidrs
  }

  inbound_rule {
    protocol         = "tcp"
    port_range       = "80"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  inbound_rule {
    protocol         = "tcp"
    port_range       = "443"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  # WireGuard (Slice 4+). Open now so cutover does not need a second firewall change.
  inbound_rule {
    protocol         = "udp"
    port_range       = "51820"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  inbound_rule {
    protocol         = "icmp"
    source_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "tcp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "udp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "icmp"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
}

# --- Cloudflare DNS (unproxied / grey-cloud) ---
# Path A: staging + future WG/AGH names. Apex nightloch.app left for Slice 11.

resource "cloudflare_record" "lite" {
  count = var.enable_dns_records ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "lite"
  type    = "A"
  content = digitalocean_droplet.nightloch.ipv4_address
  ttl     = var.dns_ttl
  proxied = false
  comment = "nightloch-lite staging (Slice 1 Path A)"
}

resource "cloudflare_record" "agh" {
  count = var.enable_dns_records ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "agh"
  type    = "A"
  content = digitalocean_droplet.nightloch.ipv4_address
  ttl     = var.dns_ttl
  proxied = false
  comment = "AGH admin (auth later)"
}

resource "cloudflare_record" "vpn" {
  count = var.enable_dns_records ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "vpn"
  type    = "A"
  content = digitalocean_droplet.nightloch.ipv4_address
  ttl     = var.dns_ttl
  proxied = false
  comment = "WireGuard endpoint (Slice 4+)"
}

# Optional Resend domain records — paste values from Resend after adding nightloch.app.
resource "cloudflare_record" "resend_spf" {
  count = var.manage_resend_dns ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "@"
  type    = "TXT"
  content = var.resend_spf_include
  ttl     = var.dns_ttl
  comment = "Resend SPF"
}

resource "cloudflare_record" "resend_dkim" {
  count = var.manage_resend_dns && var.resend_dkim_value != "" ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = var.resend_dkim_name
  type    = "TXT"
  content = var.resend_dkim_value
  ttl     = var.dns_ttl
  comment = "Resend DKIM"
}

resource "cloudflare_record" "resend_domain_verify" {
  count = var.manage_resend_dns && var.resend_domain_verify_txt != "" ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "@"
  type    = "TXT"
  content = var.resend_domain_verify_txt
  ttl     = var.dns_ttl
  comment = "Resend domain verification"
}

resource "cloudflare_record" "dmarc" {
  count = var.manage_resend_dns && var.enable_dmarc ? 1 : 0

  zone_id = var.cloudflare_zone_id
  name    = "_dmarc"
  type    = "TXT"
  content = var.dmarc_rua != "" ? "v=DMARC1; p=none; rua=${var.dmarc_rua}" : "v=DMARC1; p=none"
  ttl     = var.dns_ttl
  comment = "DMARC (start with p=none)"
}

locals {
  fixture_name = "${var.environment}-lsp"
  common_tags = {
    purpose = "language-server-fixture"
  }
}

# Changelog

## 0.2.0 - 2026-07-12

- Added an interactive terminal control panel.
- Added automatic creation and atomic saving of per-instance JSON configuration files.
- Added automatic generation or secure entry of per-instance shared secrets.
- Added automatic systemd enable/start after setup.
- Added interactive editing, deletion, list, status, logs, start, stop, and restart operations.
- Added direct administration commands for automation.
- Added automatic selection of an unused default TUN interface name.
- Added validation of all configured instances from the panel.
- Updated the installer to offer launching the setup panel after installation.
- Added setup-wizard tests with a simulated systemd command.
- Updated Persian installation and operations documentation.

## 0.1.0 - 2026-07-12

- Initial public version.
- Encrypted Layer 3 TUN packet transport.
- Layer 4 TCP and UDP service forwarding.
- TCP, reliable UDP, and preferred/standby `both` transport mode with automatic failover.
- Authenticated peer handshake with bidirectional key confirmation.
- AES-256-GCM data protection and HMAC-SHA256 UDP acknowledgements.
- UDP fragmentation and reassembly.
- Multi-instance systemd template.
- Prebuilt Linux binaries for amd64, arm64, and armv7 with checksums.
- Persian installation, configuration, security, and troubleshooting guide.

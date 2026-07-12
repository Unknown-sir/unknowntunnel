# Security policy

Please report security vulnerabilities privately to the repository owner instead
of opening a public issue. Include the affected version, configuration, impact,
and a minimal reproducer when possible.

Unknowntunnel authenticates peers with a pre-shared secret and encrypts tunnel
traffic with AES-256-GCM. Protect the secret file with mode 0600, use a randomly
generated secret, restrict transport ports with a firewall, and keep the host OS
and Go toolchain updated.

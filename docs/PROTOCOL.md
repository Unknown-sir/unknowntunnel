# Unknowntunnel protocol notes

This document describes the version 1 wire protocol used by Unknowntunnel 0.2.0. The terminal management features added in 0.2.0 do not change the wire format.
It is intended for maintainers and interoperability reviews. The implementation
in `internal/transport` remains the normative reference.

## Peer model

Each configuration names exactly one local node and one expected peer. A server
accepts connections only when the authenticated `node_id` and `peer_id` are the
configured inverse pair.

## Handshake

Both TCP and UDP transports start with an authenticated handshake containing:

- protocol magic and version;
- handshake type;
- Unix timestamp;
- a 32-byte random nonce;
- local and expected peer identifiers;
- HMAC-SHA256 over the handshake fields.

The server response includes a second random nonce and is bound to the client
nonce. Timestamps outside a five-minute window are rejected.

After key derivation, both sides exchange encrypted key-confirmation messages.
A transport is not exposed to the application until both confirmations succeed.
This prevents a captured authenticated hello from replacing a live session
without proving possession of the derived session keys.

## Key derivation

The shared secret is used as input keying material. Client nonce, server nonce,
and both node identifiers form the salt context. HKDF-SHA256 derives independent
32-byte keys for:

- client-to-server data;
- server-to-client data;
- client-to-server UDP acknowledgements;
- server-to-client UDP acknowledgements.

Data keys are never reused across sessions because both sides contribute fresh
random nonces.

## Encryption

Application messages are encrypted with AES-256-GCM. Each direction has an
independent monotonically increasing sequence number and key. The sequence
number is authenticated as associated data and is also used to construct the
12-byte GCM nonce.

## TCP carrier

The TCP carrier is a persistent encrypted record stream. Every record contains:

- encrypted record length;
- sequence number;
- AES-GCM ciphertext and authentication tag.

Records must arrive in exact sequence order. Concurrent application writers are
serialized before sequence allocation and encryption.

## UDP carrier

The UDP carrier implements an encrypted reliable ordered datagram channel:

- every data packet has a sequence number;
- every accepted packet receives an authenticated ACK;
- unacknowledged packets are retransmitted;
- duplicate packets are discarded;
- out-of-order packets are held in a bounded reorder window;
- sustained missing acknowledgements close the session and trigger reconnect;
- pending retransmissions are bounded and apply backpressure.

ACKs use a direction-specific HMAC-SHA256 key truncated to 128 bits. Data uses
AES-256-GCM.

## Application messages

The encrypted application protocol carries these message types:

- IP packet from a TUN interface;
- open TCP service flow;
- TCP open status;
- TCP stream data;
- TCP flow close;
- UDP datagram fragment;
- ping and pong.

Every application message has a unique identifier. In `both` mode, TCP and UDP
sessions remain connected at the same time, but a message is sent only over the
configured preferred carrier. If that send fails, the same encoded message is
immediately attempted on the standby carrier. The receiver still keeps a bounded
duplicate window as a defensive replay and duplication guard.

TCP stream chunks have an additional per-flow sequence number, so stream byte
order remains correct if the carrier changes during a flow. UDP datagrams have a
datagram ID and fragment index and are reassembled before delivery.

## Service authorization

An L4 open request contains only a configured service name. It does not contain
an arbitrary destination address. The receiving node resolves that name through
its local `services` allowlist and rejects missing or protocol-mismatched names.

## Limits

Retransmission queues, reorder windows, partial UDP datagrams, message sizes, and
duplicate-message retention are bounded. UDP flows expire after an idle timeout.
The number of simultaneous TCP flows and accepted sockets is still governed by
host file-descriptor, memory, firewall, and service limits, so operators should
apply OS-level connection limits appropriate for their deployment.

The protocol and cryptographic construction have not received a formal independent
audit. The implementation should be reviewed before use in highly sensitive
environments.

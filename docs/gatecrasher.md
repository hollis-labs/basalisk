# Approvals from Gatecrasher

Gatecrasher is the portfolio's approval service. When it is configured, Basalisk shows
the approvals that wait on the owner as cards in the assistant's panel, counts them on
the bar's "for you" chip and on the Dock icon, and sends the owner's decisions back.

## Configure

Create `~/.config/basalisk/gatecrasher.json`:

```json
{ "url": "https://gatecrasher.example", "key_file": "~/.local/share/basalisk/secrets/gatecrasher-owner.key" }
```

- `url`: Gatecrasher's base URL.
- `key_file`: the owner's Gatecrasher credential (a bearer key with
  `gatecrasher.approval.read` and `gatecrasher.approval.decide`, resolving to the
  `approvals.owner.principal` Gatecrasher is configured with). Optional; the default is
  `gatecrasher-owner.key` in Basalisk's secrets directory. Keep it 0600. It is read on
  each use, so a rotated key is picked up without a restart.

Without the file, approvals are off and nothing is shown. The file is read at start: restart
Basalisk after creating or changing it.

## How it works

- The server (`internal/gatecrasher`) follows Gatecrasher's event stream
  (`GET /api/gate/v1/events`) as the owner and reads each approval's card. It keeps the
  stream's position and the cards in `gatecrasher-state.json` in the data directory
  (0600), so a restart resumes where it stopped (`Last-Event-ID`) without duplicate cards.
  A dropped stream reconnects with backoff from the same place.
- The page reads the cards by long poll (`GET /api/gate/cards?v=<version>`), so a new
  approval shows within a moment.
- A decision goes through `POST /api/gate/cards/{id}/decide` (`outcome`, and `choice`,
  `answer`, `note` or `narrowed` as the card asks). Any other field is refused.
- Decided cards stay for 15 minutes, showing how they ended.

## Secrets

A `request_secret` card has a one-field form. The value is posted from the page
straight to the card's intake URL (the producer, for example Warden). It never reaches
Basalisk's server, the assistant, a log, `agent.db` or any store, and the field is cleared
as soon as it is sent. The card closes when the producer tells Gatecrasher the secret
arrived. A secret request cannot be approved through decide; it can only be declined.

The assistant does not see approvals at all.

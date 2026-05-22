# Codex reset quota API capture (sanitized)

## Request

```http
POST https://codex.rayplus.site/api/subscriptions/1716/reset-quota
Authorization: Bearer <ACCESS_TOKEN>
Content-Type: application/json
Accept: application/json
Content-Length: 0
```

The service first calls `GET /api/subscriptions`, then resets either the
configured `SUBSCRIPTION_ID` or the first `active` subscription with
`canReset=true`.

# RayPlus login API capture (sanitized)

## Request

```http
POST https://rayplus.site/api/v1/auth/login
Content-Type: application/json
Accept: application/json
```

```json
{
  "email": "<RAYPLUS_EMAIL>",
  "password": "<RAYPLUS_PASSWORD>"
}
```

## Response

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "access_token": "<ACCESS_TOKEN>",
    "refresh_token": "<REFRESH_TOKEN>",
    "expires_in": 86400,
    "token_type": "Bearer",
    "user": {
      "id": 1859,
      "email": "<RAYPLUS_EMAIL>",
      "balance": -0.002658,
      "status": "active"
    }
  }
}
```

Use `access_token` as `Authorization: Bearer <ACCESS_TOKEN>` for Codex subscription APIs.

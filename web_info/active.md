# RayPlus active subscriptions API capture (sanitized)

## Request

```http
GET https://rayplus.site/api/v1/subscriptions/active?timezone=Asia%2FShanghai
Authorization: Bearer <ACCESS_TOKEN>
Accept: application/json
```

## Response

```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "id": 1716,
      "user_id": 1859,
      "group_id": 20,
      "status": "active",
      "weekly_usage_usd": 8.6042286,
      "monthly_usage_usd": 8.6042286,
      "group": {
        "id": 20,
        "name": "<GROUP_NAME>",
        "platform": "openai",
        "subscription_type": "subscription",
        "daily_limit_usd": 100
      }
    }
  ]
}
```

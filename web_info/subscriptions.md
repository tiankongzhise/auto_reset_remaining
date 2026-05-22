# Codex subscriptions API capture (sanitized)

## Request

```http
GET https://codex.rayplus.site/api/subscriptions
Authorization: Bearer <ACCESS_TOKEN>
Accept: application/json
```

## Response

```json
{
  "subscriptions": [
    {
      "id": 1716,
      "userId": 1859,
      "groupId": 20,
      "status": "active",
      "remainingDays": 29,
      "dailyUsage": 0,
      "weeklyUsage": 8.6042286,
      "monthlyUsage": 8.6042286,
      "resetCountToday": 0,
      "resetLimitPerDay": 6,
      "group": {
        "id": 20,
        "name": "<GROUP_NAME>",
        "platform": "openai",
        "subscriptionType": "subscription",
        "dailyLimit": 100
      },
      "canReset": true
    }
  ]
}
```

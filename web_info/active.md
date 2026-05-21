https://rayplus.site/api/v1/subscriptions/active?timezone=Asia%2FShanghai

reuqest header
:authority
rayplus.site
:method
GET
:path
/api/v1/subscriptions/active?timezone=Asia%2FShanghai
:scheme
https
accept
application/json, text/plain, */*
accept-encoding
gzip, deflate, br, zstd
accept-language
zh
authorization
Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxODU5LCJlbWFpbCI6ImNzYmFpY2hlbmdlZHVAMTYzLmNvbSIsInJvbGUiOiJ1c2VyIiwidG9rZW5fdmVyc2lvbiI6NTUyNzcwMDcxMzEwNzY0NzQxOSwiZXhwIjoxNzc5NDYxODk1LCJuYmYiOjE3NzkzNzU0OTUsImlhdCI6MTc3OTM3NTQ5NX0.N28r9l2ukHY-FcxmAvNBNdCcEQw8lN_oVJjYP292KAo
cookie
__stripe_mid=c7a8a8de-14bc-4caf-a13c-ec247edbad7650d08e; cf_clearance=wFkpN0AQktsDCg5l.Jul5qsXNmP9Fx5SaZCGO2vltho-1779401835-1.2.1.1-OAzDPj55dAxDEmtnWhHbmPeXxmIq9vfo3_8UunpDFo_3vPznRqpoHTGlsDwMJG5NazqQHrkMBiyYPWYgwT6i.YnuPLcMauTDpllhoEtQVpBZBW9yJSbSOqfEN6wWLA1gm7OECu_TC8lDAdQxGuAcisSt7J5YCMrmA76ghMy8rQodTO8NDJW6Hpv2IvRYP7ODO7a4bzPMR2QaN9eMcCm4.gaafWXSeqt.K.oI.8TuZsuKzt7MIGh6VYTrcf5h5uyPqGhNmp0Orpj3E.DhtxrtObRPhSqzJQ9iLTtpDfKewUD0iQMI7kjlQBLa4HLedTRqam1SEgTfA8_E7iuzSNGRFA; __stripe_sid=30637f1e-7dc8-4fd6-9569-90779ce48c4862b0f6
priority
u=1, i
referer
https://rayplus.site/custom/63b2443f250995c3
sec-ch-ua
"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"
sec-ch-ua-mobile
?0
sec-ch-ua-platform
"Windows"
sec-fetch-dest
empty
sec-fetch-mode
cors
sec-fetch-site
same-origin
user-agent
Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36

response
{
    "code": 0,
    "message": "success",
    "data": [
        {
            "id": 1716,
            "user_id": 1859,
            "group_id": 20,
            "starts_at": "2026-05-21T12:14:31.038792+08:00",
            "expires_at": "2026-06-19T12:14:31.038792+08:00",
            "status": "active",
            "daily_window_start": null,
            "weekly_window_start": "2026-05-21T00:00:00+08:00",
            "monthly_window_start": "2026-05-21T00:00:00+08:00",
            "daily_usage_usd": 0,
            "weekly_usage_usd": 8.6042286,
            "monthly_usage_usd": 8.6042286,
            "created_at": "2026-05-21T12:14:31.0388+08:00",
            "updated_at": "2026-05-21T21:34:56.969776+08:00",
            "group": {
                "id": 20,
                "name": "置顶codex中转可用code5.5每日100刀",
                "description": "",
                "platform": "openai",
                "rate_multiplier": 1,
                "is_exclusive": true,
                "status": "active",
                "subscription_type": "subscription",
                "daily_limit_usd": 100,
                "weekly_limit_usd": 0,
                "monthly_limit_usd": 0,
                "allow_image_generation": true,
                "image_rate_independent": false,
                "image_rate_multiplier": 1,
                "image_price_1k": null,
                "image_price_2k": null,
                "image_price_4k": null,
                "claude_code_only": false,
                "fallback_group_id": null,
                "fallback_group_id_on_invalid_request": null,
                "allow_messages_dispatch": true,
                "require_oauth_only": false,
                "require_privacy_set": false,
                "rpm_limit": 0,
                "created_at": "2026-03-12T04:00:22.842823+08:00",
                "updated_at": "2026-05-13T21:10:22.69143+08:00"
            }
        }
    ]
}
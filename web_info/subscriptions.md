
https://codex.rayplus.site/api/subscriptions

:authority
codex.rayplus.site
:method
GET
:path
/api/subscriptions
:scheme
https
accept
*/*
accept-encoding
gzip, deflate, br, zstd
accept-language
zh-CN,zh;q=0.9
authorization
Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxODU5LCJlbWFpbCI6ImNzYmFpY2hlbmdlZHVAMTYzLmNvbSIsInJvbGUiOiJ1c2VyIiwidG9rZW5fdmVyc2lvbiI6NTUyNzcwMDcxMzEwNzY0NzQxOSwiZXhwIjoxNzc5NDYxODk1LCJuYmYiOjE3NzkzNzU0OTUsImlhdCI6MTc3OTM3NTQ5NX0.N28r9l2ukHY-FcxmAvNBNdCcEQw8lN_oVJjYP292KAo
content-type
application/json
cookie
__stripe_mid=c7a8a8de-14bc-4caf-a13c-ec247edbad7650d08e; cf_clearance=wFkpN0AQktsDCg5l.Jul5qsXNmP9Fx5SaZCGO2vltho-1779401835-1.2.1.1-OAzDPj55dAxDEmtnWhHbmPeXxmIq9vfo3_8UunpDFo_3vPznRqpoHTGlsDwMJG5NazqQHrkMBiyYPWYgwT6i.YnuPLcMauTDpllhoEtQVpBZBW9yJSbSOqfEN6wWLA1gm7OECu_TC8lDAdQxGuAcisSt7J5YCMrmA76ghMy8rQodTO8NDJW6Hpv2IvRYP7ODO7a4bzPMR2QaN9eMcCm4.gaafWXSeqt.K.oI.8TuZsuKzt7MIGh6VYTrcf5h5uyPqGhNmp0Orpj3E.DhtxrtObRPhSqzJQ9iLTtpDfKewUD0iQMI7kjlQBLa4HLedTRqam1SEgTfA8_E7iuzSNGRFA; __stripe_sid=30637f1e-7dc8-4fd6-9569-90779ce48c4862b0f6
if-none-match
W/"2f9-rTHGIGyviGGz+UJe2k0IC++HZCc"
priority
u=1, i
referer
https://codex.rayplus.site/_u/reset-quota-4f8a2c17d9e3?user_id=1859&token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxODU5LCJlbWFpbCI6ImNzYmFpY2hlbmdlZHVAMTYzLmNvbSIsInJvbGUiOiJ1c2VyIiwidG9rZW5fdmVyc2lvbiI6NTUyNzcwMDcxMzEwNzY0NzQxOSwiZXhwIjoxNzc5NDYxODk1LCJuYmYiOjE3NzkzNzU0OTUsImlhdCI6MTc3OTM3NTQ5NX0.N28r9l2ukHY-FcxmAvNBNdCcEQw8lN_oVJjYP292KAo&theme=light&lang=zh&ui_mode=embedded&src_host=https%3A%2F%2Frayplus.site&src_url=https%3A%2F%2Frayplus.site%2Fcustom%2F63b2443f250995c3
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

{
    "subscriptions": [
        {
            "id": 1716,
            "userId": 1859,
            "groupId": 20,
            "startsAt": "2026-05-21T04:14:31.038Z",
            "expiresAt": "2026-06-19T04:14:31.038Z",
            "status": "active",
            "remainingDays": 29,
            "dailyUsage": 0,
            "weeklyUsage": 8.6042286,
            "monthlyUsage": 8.6042286,
            "dailyWindowStart": null,
            "weeklyWindowStart": "2026-05-20T16:00:00.000Z",
            "monthlyWindowStart": "2026-05-20T16:00:00.000Z",
            "createdAt": "2026-05-21T04:14:31.038Z",
            "updatedAt": "2026-05-21T13:34:56.969Z",
            "resetCountToday": 0,
            "resetLimitPerDay": 6,
            "resetRecords": [
                {
                    "id": 2058,
                    "userId": 1859,
                    "resetAt": "2026-05-21T13:24:14.807Z"
                }
            ],
            "group": {
                "id": 20,
                "name": "置顶codex中转可用code5.5每日100刀",
                "description": "",
                "platform": "openai",
                "subscriptionType": "subscription",
                "dailyLimit": 100,
                "weeklyLimit": 0,
                "monthlyLimit": 0
            },
            "canReset": true
        }
    ]
}
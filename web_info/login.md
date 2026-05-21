Request URL
https://rayplus.site/api/v1/auth/login
Request Method
POST
Status Code
200 OK
Remote Address
127.0.0.1:10808
Referrer Policy
strict-origin-when-cross-origin

response headers:"
access-control-allow-headers
Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-API-Key, x-stainless-lang, x-stainless-package-version, x-stainless-os, x-stainless-arch, x-stainless-retry-count, x-stainless-runtime, x-stainless-runtime-version, x-stainless-async, x-stainless-helper-method, x-stainless-poll-helper, x-stainless-custom-poll-interval, x-stainless-timeout
access-control-allow-methods
POST, OPTIONS, GET, PUT, DELETE, PATCH
access-control-allow-origin
*
access-control-expose-headers
ETag
access-control-max-age
86400
cf-cache-status
DYNAMIC
cf-ray
9ff4702aeabdfce3-SIN
content-encoding
zstd
content-security-policy
default-src 'self'; script-src 'self' 'nonce-TtoxW/e9jJ6Sv9rDvJDj9w==' https://challenges.cloudflare.com https://static.cloudflareinsights.com https://*.stripe.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; img-src 'self' data: https:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' https:; frame-src https://challenges.cloudflare.com https://*.stripe.com https://checkout.airwallex.com https://checkout-demo.airwallex.com https://codex.rayplus.site; frame-ancestors 'none'; base-uri 'self'; form-action 'self'
content-type
application/json; charset=utf-8
date
Thu, 21 May 2026 14:58:15 GMT
nel
{"report_to":"cf-nel","success_fraction":0.0,"max_age":604800}
referrer-policy
strict-origin-when-cross-origin
report-to
{"group":"cf-nel","max_age":604800,"endpoints":[{"url":"https://a.nel.cloudflare.com/report/v4?s=ibE8HZnpvGW2gtHr6bpiu96KP37Hq%2BuFDVm5D351nD4f2qPxLcrL3GVk%2BrHsuoTrBtVTvVBEzdyAPlhTlbzPpb87AWHPjL7wQzDVVqAoOqPY7G3zYf85%2BCd227AKSQg%3D"}]}
server
cloudflare
x-content-type-options
nosniff
x-frame-options
DENY

x-request-id
b35bd487-fd47-47fb-97a2-3bad932e4c2e
x-zeabur-ip-country
DE
x-zeabur-request-id
6d16b6b4-54fe-45da-8523-0b3f355d2411
"

request headers:"
:authority
rayplus.site
:method
POST
:path
/api/v1/auth/login
:scheme
https
accept
application/json, text/plain, */*
accept-encoding
gzip, deflate, br, zstd
accept-language
zh
content-length
63
content-type
application/json
cookie
__stripe_mid=c7a8a8de-14bc-4caf-a13c-ec247edbad7650d08e; __stripe_sid=f9ed7193-b7b4-4f48-938e-ca147e55b1012ce8a7; cf_clearance=Fz0EvkMU1c3scx0r4NCsi9Xwq5Yi8lh4fdF2bDWN6.c-1779375479-1.2.1.1-4H.LGnkVBJ7.WhLNBTXT43DnGr2YMqJm4c73Os8qobzqM1NumuY9uEyHsbMni.WOYu9XihXaQtzrjLmpC4JRx89vrtyI5ysij6aPxyQUO_TRyTpVs8Fk72mHulwwnJyERVKB6UtyUmVyqprcviYG7QTg4hLSjq6IDzAdVTbeaOYiG67phH6PY8N8XVKJPFee7PN5pbSbcxz96ryRBGuA33dtKb.wXi1xHV5doVEIjhZxkb2ZRlI4y5ZeeKRPTNd6kOfOPXLf1KpZbRGlXbvAGSTQs8DV9OsfXjZWAIss1f3frPkvjnJGvcy41cv1M8Ib2G82mVmThSV9j.EwXKOULQ
origin
https://rayplus.site
priority
u=1, i
referer
https://rayplus.site/login
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
Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

payload:
{"email":"csbaichengedu@163.com","password":"9tdSB_E2F9Bo2Zey"}

response:
{
    "code": 0,
    "message": "success",
    "data": {
        "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxODU5LCJlbWFpbCI6ImNzYmFpY2hlbmdlZHVAMTYzLmNvbSIsInJvbGUiOiJ1c2VyIiwidG9rZW5fdmVyc2lvbiI6NTUyNzcwMDcxMzEwNzY0NzQxOSwiZXhwIjoxNzc5NDYxODk1LCJuYmYiOjE3NzkzNzU0OTUsImlhdCI6MTc3OTM3NTQ5NX0.N28r9l2ukHY-FcxmAvNBNdCcEQw8lN_oVJjYP292KAo",
        "refresh_token": "rt_4ce4802875058a962a95a9531d6ee61bb864417b5b9cf3a17dff57284bbff762",
        "expires_in": 86400,
        "token_type": "Bearer",
        "user": {
            "id": 1859,
            "email": "csbaichengedu@163.com",
            "username": "",
            "role": "user",
            "balance": -0.002658,
            "concurrency": 5,
            "status": "active",
            "allowed_groups": null,
            "last_active_at": "2026-05-21T22:11:34.227052+08:00",
            "created_at": "2026-05-18T16:52:59.728473+08:00",
            "updated_at": "2026-05-21T22:11:34.227059+08:00",
            "balance_notify_enabled": true,
            "balance_notify_threshold_type": "fixed",
            "balance_notify_threshold": null,
            "balance_notify_extra_emails": null,
            "total_recharged": 600,
            "rpm_limit": 0
        }
    }
}
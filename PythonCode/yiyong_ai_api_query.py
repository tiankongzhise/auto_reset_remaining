#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Yiyong Ai API 查询脚本

用途：
    使用 Yiyong Ai 用户后台创建的 API Key，查询当前密钥可用的模型列表、
    用量统计、余额或额度信息。

使用方法：
    1. 在下方配置 API_KEY。
    2. 在下方配置 BASE_URL，例如：https://rayplus.site
    3. 运行：
           python yiyong_ai_api_query.py

接口说明：
    GET /v1/models
        查询当前 API Key 可用的模型列表。

    GET /v1/usage
        查询当前 API Key 对应的用量、余额或额度信息。

认证方式：
    Authorization: Bearer <API_KEY>

注意事项：
    - API Key 需要在用户后台的“API 密钥”页面创建。
    - API Key 需要处于启用状态。
    - 如果密钥没有绑定可用分组，接口可能返回 403。
    - 如果站点前面启用了 Cloudflare，部分规则可能会拦截脚本请求；
      本脚本已内置常见浏览器 User-Agent，但更推荐站点管理员放行 API 路径。
"""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.request
from typing import Any


# 必填：填写用户后台创建的 API Key。
API_KEY = "sk-3ef1de3c9208c22ecc0f1a313698cc60f2533ea5c98160538e9562f0b1453e74"

# 必填：填写 Yiyong Ai API 服务地址，末尾有没有斜杠都可以。
BASE_URL = "https://rayplus.site/"

# 模拟常见浏览器请求头，避免被简单的 User-Agent 规则误拦。
USER_AGENT = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
    "AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/125.0.0.0 Safari/537.36"
)


def request_json(path: str) -> Any:
    """请求指定 Yiyong Ai API 接口，成功时返回 JSON，失败时返回 None。"""
    url = f"{BASE_URL.rstrip('/')}{path}"
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Accept": "application/json",
            "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
            "Cache-Control": "no-cache",
            "Pragma": "no-cache",
            "User-Agent": USER_AGENT,
        },
        method="GET",
    )

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            return json.loads(body) if body else None
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        print(f"\n[错误] GET {path} -> HTTP {exc.code}", file=sys.stderr)
        print(body, file=sys.stderr)
        if exc.code == 403 and "cloudflare_error" in body and '"error_code":1010' in body:
            print(
                "\nCloudflare 在请求到达 Yiyong Ai API 后端前拦截了本次访问。"
                "如果加了浏览器 User-Agent 后仍然出现这个错误，"
                "请让站点管理员在 Cloudflare 中放行 /v1/*、/v1beta/* 等 API 路径，"
                "或者使用一个单独的 API 专用域名。",
                file=sys.stderr,
            )
        return None
    except urllib.error.URLError as exc:
        print(f"\n[错误] GET {path} 请求失败：{exc}", file=sys.stderr)
        return None
    except json.JSONDecodeError as exc:
        print(f"\n[错误] GET {path} 返回内容不是有效 JSON：{exc}", file=sys.stderr)
        return None


def print_section(title: str, data: Any) -> None:
    """用统一格式打印接口返回结果。"""
    print(f"\n=== {title} ===")
    if data is None:
        print("没有数据。")
        return
    print(json.dumps(data, ensure_ascii=False, indent=2))


def main() -> int:
    if not API_KEY or API_KEY == "sk-your-api-key-here":
        print("请先在脚本中填写 API_KEY。", file=sys.stderr)
        return 2
    if not BASE_URL or "your-api-domain.com" in BASE_URL:
        print("请先在脚本中填写 BASE_URL。", file=sys.stderr)
        return 2

    models = request_json("/v1/models")
    print_section("可用模型", models)

    usage = request_json("/v1/usage")
    print_section("用量 / 余额 / 额度", usage)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())

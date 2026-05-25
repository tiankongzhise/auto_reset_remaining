# auto_reset_remaining Python 本地版

这是一个面向 Windows 本地运行的 Python 小工具，用来监控 RayPlus API 余额，并在余额不足时辅助重置 Codex 订阅额度。

本项目功能参考自原项目：[tiankongzhise/auto_reset_remaining](https://github.com/tiankongzhise/auto_reset_remaining)。

## 当前目标

- 使用 `uv` 管理 Python 环境和依赖。
- 使用 Tkinter 提供简单本地 UI。
- 使用 SQLite 保存本地记录。
- 使用 Windows 本地弹窗代替 Web 服务和邮件系统。

## 运行方式

```powershell
uv run auto-reset-remaining
```

首次启动会基于 `config.example.toml` 生成本地 `config.toml`。`config.toml` 可能包含账号、密码和 API key，不会提交到 Git。

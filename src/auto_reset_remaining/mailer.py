from __future__ import annotations

import smtplib
import ssl
from email.message import EmailMessage

from .config import SMTPConfig


class SMTPMailer:
    def __init__(self, config: SMTPConfig) -> None:
        self.config = config

    def send(self, subject: str, body: str) -> None:
        if not self.config.to:
            raise ValueError("SMTP_TO is empty")

        message = EmailMessage()
        message["From"] = self.config.from_addr
        message["To"] = ", ".join(self.config.to)
        message["Subject"] = subject
        message.set_content(body, charset="utf-8")

        if self.config.port == 465:
            context = ssl.create_default_context()
            with smtplib.SMTP_SSL(self.config.host, self.config.port, context=context, timeout=30) as client:
                self._login_if_needed(client)
                client.send_message(message)
            return

        with smtplib.SMTP(self.config.host, self.config.port, timeout=30) as client:
            client.ehlo()
            if client.has_extn("STARTTLS"):
                client.starttls(context=ssl.create_default_context())
                client.ehlo()
            self._login_if_needed(client)
            client.send_message(message)

    def _login_if_needed(self, client: smtplib.SMTP) -> None:
        if self.config.user:
            client.login(self.config.user, self.config.password)

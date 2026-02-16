import os
import sys
import asyncio
from telethon import TelegramClient

SESSION_FILE = os.environ.get("SESSION_FILE", os.path.expanduser("~/.telethon_test_session"))
API_ID = int(os.environ["TG_API_ID"])
API_HASH = os.environ["TG_API_HASH"]
BOT_USERNAME = os.environ.get("BOT_USERNAME", "autonous_bot")


async def main():
    message = sys.argv[1] if len(sys.argv) > 1 else "hello"
    client = TelegramClient(SESSION_FILE, API_ID, API_HASH)
    await client.start()
    await client.send_message(BOT_USERNAME, message)
    print(f"sent to @{BOT_USERNAME}: {message}")
    await client.disconnect()


asyncio.run(main())

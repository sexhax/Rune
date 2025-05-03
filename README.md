# Discord Selfbot

A simple Discord selfbot that responds to commands from the owner.

## Setup

1. Clone this repository
2. Edit the `config.json` file with your Discord token and user ID:
   ```json
   {
       "token": "YOUR_DISCORD_TOKEN_HERE",
       "owner_id": "YOUR_DISCORD_USER_ID_HERE",
       "prefix": "&"
   }
   ```
3. Build and run the selfbot:
   ```bash
   go build
   ./selfbot
   ```

## Commands

All commands use the prefix `&` by default (can be changed in config.json):

- `&help` - Shows all available commands
- `&ping` - Checks if the selfbot is responding
- `&avatar` - Shows your avatar or the avatar of a mentioned user
- `&userinfo` - Shows information about you or a mentioned user
- `&say [message]` - Makes the selfbot say something
- `&clear [count]` - Deletes a specified number of messages (default: 10, max: 100)

## Warning

**Self-bots are against Discord's Terms of Service and could result in your account being banned. Use at your own risk.**

## License

This project is provided for educational purposes only. 
# Web Client: Consider authentication

The web client is wide open right now. I'm assuming I'll use it over Tailscale or similar, but it could be good to implement some kind of authentication. Maybe something dumb like secret token auth, randomized at server startup and/or specified in config file?
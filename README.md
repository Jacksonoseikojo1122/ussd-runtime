# USSD Runtime

Run the USSD menus you design in USSD Flow Studio, fast enough for a real phone session.

> **Status: in development.** I'm building this in public. Follow along here or on the project page:
> **https://jacksonkojoosei.com/projects/ussd-runtime**

## What it does

A hosted engine that runs USSD flows exported from my USSD Flow Studio. It keeps session state in Redis and answers within the few seconds a mobile network allows before it drops the session.

## Why

USSD still reaches people without smartphones or data, but every network gives you only a few seconds per reply. Slow servers mean dropped sessions and lost customers.

## Planned features

- [ ] Runs flows exported from USSD Flow Studio
- [ ] Session state in Redis with timeouts
- [ ] Adapters for common USSD gateway formats
- [ ] Load tests with published latency numbers

## Stack

Go, Redis, Docker

## Author

Built by **[Jackson Kojo Osei](https://jacksonkojoosei.com)**, CTO in Accra, Ghana.
[LinkedIn](https://www.linkedin.com/in/jackson-kojo-osei-740846189) · [All projects](https://jacksonkojoosei.com/projects)

## License

MIT

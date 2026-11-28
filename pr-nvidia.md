## Problem

The LLM profile form requires users to manually enter the base URL for OpenAI-compatible gateways. There is no quick way to select common endpoints like NVIDIA NIM.

## Change

Adds a neutral preset dropdown next to the Base URL field when the **OpenAI** format is selected:

- **Custom** — user enters their own base URL (default)
- **NVIDIA NIM** — pre-fills `https://integrate.api.nvidia.com/v1`

No vendor logos, promotional codes, or baked endpoints — just a name and URL. The preset is a UI convenience only; the underlying format remains "openai".

Updates the README with the NVIDIA NIM base URL example in the OpenAI-compatible gateways section.

## Testing

- Manual verification: open the LLM profile drawer, select "OpenAI (Chat Completions)" format, and confirm the preset dropdown appears with both options.
- Selecting "NVIDIA NIM" fills the Base URL field with the correct endpoint.
- The existing OpenAI format behavior is unchanged when "Custom" is selected.

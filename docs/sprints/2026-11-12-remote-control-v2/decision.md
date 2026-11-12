# Remote control v2 decision

Source: Andrew, direct implementation request, 2026-11-12, and
`docs/REMOTE-CONTROL-V2-SPEC.md`. Implement the supplied scope. Remote browser
sign-in lasts exactly 48 hours from authentication without extension, including
routine restart and upgrade. Local browser and API-token policy is unchanged.

Success is the spec acceptance matrix AUTH-1–5, PHONE-1–2, FORM-1, ACTION-1,
UI-1 and LIFE-1, with automated, simulated, reported and actual device evidence
identified separately. No shared daemon or live Tailscale changes, PR publication,
merge or release are authorized. The original draft remains preserved.

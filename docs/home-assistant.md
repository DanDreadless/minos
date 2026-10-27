# Home Assistant

[Home Assistant](https://www.home-assistant.io/) is a popular smart-home
hub. Connecting Minos to it lets you see Minos's counters there, pause
blocking from a dashboard or voice assistant, and switch a device's internet
off on a schedule (bedtime for the children's tablet, say). The easy way
uses MQTT — a small messaging service Home Assistant offers as an add-on —
and needs no configuration files at all.

## MQTT discovery (recommended)

Minos announces itself to Home Assistant over MQTT, so a **Minos** device
appears with its entities and no YAML at all.

1. In Home Assistant, install the **Mosquitto broker** add-on and the
   **MQTT** integration (Home Assistant offers it once the add-on runs).
2. Make a Home Assistant user for Minos (Settings → People → Users) — the
   Mosquitto add-on accepts Home Assistant users as broker logins.
3. In Minos, open **Settings → Home Assistant**, enter the broker as
   `mqtt://<Home Assistant's address>:1883` with that username and
   password, tick **Connect to Home Assistant** and save. The status line
   turns green when Minos is connected.

| Entity | What it does |
|---|---|
| `switch.minos_blocking` | On = blocking; off pauses blocking until turned back on |
| `button.minos_pause_blocking_for_5_minutes`, `…_for_1_hour` | Timed pauses |
| `sensor.minos_queries`, `sensor.minos_blocked_queries` | Counted since Minos started (`total_increasing`) |
| `sensor.minos_share_blocked` | Blocked queries as a percentage of all queries |
| `sensor.minos_blocklist_rules` | Rules in force (diagnostic) |
| `binary_sensor.minos_upstream_resolver_problem` | On while an upstream resolver is failing |
| `binary_sensor.minos_threat_suspected` | On while threat detection has an open finding |
| `binary_sensor.minos_blocking_may_have_broken_something` | On while a "something may be broken" suggestion is open |
| `update.minos_update` | Installed and latest version — only when *Check for new releases* is on |
| `switch.minos_block_<device>` | One per device you have named; on blocks all its DNS |

State is published when it changes and every 30 seconds; Minos's last will
marks every entity unavailable if it stops without saying goodbye. Turning
the connection off removes the entities. Commands are plain MQTT, so they
work from anything that can publish:

| Topic | Payload |
|---|---|
| `minos/blocking/set` | `ON` (resume) or `OFF` (pause until resumed) |
| `minos/pause/set` | Minutes to pause, e.g. `30` |
| `minos/device/<id>/set` | `ON` (block) or `OFF` |

Commands must not be retained — a retained one is ignored, rather than
re-applied every time Minos reconnects. State is on `minos/state` (JSON),
`minos/availability` (`online`/`offline`), `minos/update` and
`minos/device/<id>/state`. `minos` is the topic prefix, changeable in
Settings for a second Minos on the same broker.

A bedtime automation, with the device named "Kids tablet" in Minos:

```yaml
automation:
  - alias: Kids tablet off the internet at bedtime
    triggers:
      - trigger: time
        at: "21:00:00"
    actions:
      - action: switch.turn_on
        target:
          entity_id: switch.minos_block_kids_tablet
```

## REST recipes

Without a broker, Home Assistant's built-in `rest` platforms work against
the [REST API](api.md) instead. Replace `192.168.1.2:8080` and the token
throughout.

## A switch that pauses blocking

ON means blocking is active; flipping it off pauses for 30 minutes. Built
as a template switch over the [rest commands below](#rest-commands-for-automations)
(resume is an HTTP DELETE, which the plain `rest` switch can't send), with
state from the status sensor so pauses made in the Minos UI stay in sync:

```yaml
# configuration.yaml — needs the sensor and rest_commands defined below
switch:
  - platform: template
    switches:
      minos_blocking:
        friendly_name: Minos blocking
        value_template: "{{ not state_attr('sensor.minos', 'paused') }}"
        turn_on:
          action: rest_command.minos_resume
        turn_off:
          action: rest_command.minos_pause
          data:
            duration: 30m
```

## Sensors

```yaml
sensor:
  - platform: rest
    name: Minos
    resource: http://192.168.1.2:8080/api/status
    headers:
      X-Api-Token: !secret minos_token
    value_template: "{{ value_json.queries_total }}"
    unit_of_measurement: queries
    scan_interval: 60
    json_attributes:
      - queries_blocked
      - cache_hits
      - cache_misses
      - paused
      - update_available

template:
  - sensor:
      - name: Minos block rate
        unit_of_measurement: "%"
        state: >
          {% set s = states.sensor.minos %}
          {% set total = s.state | float(0) %}
          {% if total > 0 %}
            {{ (100 * s.attributes.queries_blocked | float / total) | round(1) }}
          {% else %} 0 {% endif %}
```

## REST commands for automations

```yaml
rest_command:
  minos_pause:
    url: http://192.168.1.2:8080/api/pause
    method: post
    headers:
      X-Api-Token: !secret minos_token
    payload: '{"duration": "{{ duration | default(''30m'') }}"}'
  minos_resume:
    url: http://192.168.1.2:8080/api/pause
    method: delete
    headers:
      X-Api-Token: !secret minos_token
  minos_block_device:
    url: "http://192.168.1.2:8080/api/clients/{{ ip }}"
    method: put
    headers:
      X-Api-Token: !secret minos_token
    payload: '{"blocked": {{ blocked | default(true) | lower }}}'
```

Then in an automation: internet off for the kids' tablet at bedtime —

```yaml
automation:
  - alias: Tablet offline at bedtime
    triggers:
      - trigger: time
        at: "21:00:00"
    actions:
      - action: rest_command.minos_block_device
        data:
          ip: 192.168.1.62
          blocked: true
```

(Though for schedules, Minos's own group schedules do this without Home
Assistant — use rest_command for the ad-hoc cases.)

## Notifications into Home Assistant

Point Minos's webhook at a Home Assistant
[webhook trigger](https://www.home-assistant.io/docs/automation/trigger/#webhook-trigger)
and forward events to your phone:

```yaml
# Minos side (Settings → Notifications, or minos.yaml):
#   notifications:
#     webhook_url: http://192.168.1.10:8123/api/webhook/minos-events

automation:
  - alias: Minos events to phone
    triggers:
      - trigger: webhook
        webhook_id: minos-events
        local_only: true
    actions:
      - action: notify.mobile_app_your_phone
        data:
          title: "{{ trigger.json.title }}"
          message: "{{ trigger.json.message }}"
```

The payload shape is documented in the [API reference](api.md#notifications-outbound).
"New device on your network" arriving on your phone is the one you want.

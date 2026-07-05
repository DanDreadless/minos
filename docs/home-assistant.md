# Home Assistant recipes

Minos has no custom integration and doesn't need one: everything below
uses Home Assistant's built-in `rest` platforms against the
[REST API](api.md). Replace `192.168.1.2:8080` and the token throughout.

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

# Sailing Records

The office keeps a record of every sailing the ferry makes, from the first crossing in the morning to the last one at night. Each record is written by the crew when the ferry leaves the pier, checked by the purser when it arrives, and filed with the harbour master at the end of the week. A record that is missing a field is sent back to the crew, who complete it before the next sailing, and the office reads the records once a day to see how many passengers each route has carried.

Each sailing is recorded with the fields below.

| Field | Required | Description |
| --- | --- | --- |
| Confidence | Recommended | How sure the crew is of the count |
| Timestamp | Yes | When the sailing left the pier |

## Example

A record is written as a comment and a function:

```
# sailing record
# Port: Oban

def record(sailing):
    return sailing.id
```

The records are kept for a year.

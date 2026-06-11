package schema_protocol

const testSchemaYAML = `
version: 1
domain: test_game
tuning:
  groups_top_n: 5
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:         {type: int64, role: actor_id, pk: true, pii: true}
      server_id:         {type: int32, role: dimension}
      level:             {type: int32, role: level}
      quest_level:   {type: int32, role: stage_progress}
      coins:             {type: int64, role: balance, currency_type: coins}
      last_online_time:  {type: unix_timestamp_seconds, role: last_seen}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64,  role: balance}
glossary:
  currency_types:
    coins: "coins (test)"
  buckets:
    coins_balance:
      - {min: 0,      max: 10000,  label: "0~1w"}
      - {min: 10001,  max: 100000, label: "1~10w"}
      - {min: 100001, max: 200000, label: "10~20w"}
      - {min: 200001, max: 500000, label: "20w~50w"}
      - {min: 500001, max: null,   label: "50w+"}
`

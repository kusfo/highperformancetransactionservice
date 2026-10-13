"""Validate T02 OpenAPI structure and each embedded media-type example."""
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate

root = Path(__file__).resolve().parents[2]
document = json.loads((root / 'api/user-session.openapi.json').read_text())
validate(document)
count = 0

def visit(value):
    global count
    if isinstance(value, dict):
        if 'schema' in value and ('example' in value or 'examples' in value):
            schema = dict(value['schema'])
            schema['components'] = document['components']
            validator = Draft202012Validator(schema, format_checker=FormatChecker())
            examples = [value['example']] if 'example' in value else [entry['value'] for entry in value['examples'].values()]
            for example in examples:
                validator.validate(example)
                count += 1
        for child in value.values():
            visit(child)
    elif isinstance(value, list):
        for child in value:
            visit(child)

visit(document['paths'])
assert count > 0, 'Expected request/response examples'
assert not any(word in path for path in document['paths'] for word in ('recovery', 'verify', 'admin', 'refresh'))
print(f'OpenAPI structure and {count} embedded examples passed.')

# Guard contract boundaries that valid examples alone would not exercise.
def schema_validator(name):
    return Draft202012Validator(
        {'$ref': '#/components/schemas/' + name, 'components': document['components']},
        format_checker=FormatChecker(),
    )

negative_cases = [
    ('RegisterRequest', {'email': 'person@example.com', 'password': 'short'}),
    ('RegisterRequest', {'email': 'person@example.com', 'password': 'x' * 129}),
    ('RegisterRequest', {'email': 'person@example.com', 'password': 'x' * 12, 'displayName': ''}),
    ('RegisterRequest', {'email': 'person@example.com', 'saltedhashedpassword': 'hash'}),
    ('ProfileUpdate', {'email': 'different@example.com'}),
    ('ProfileUpdate', {'displayName': 'x' * 101}),
    ('PasswordChange', {'newPassword': 'x' * 12}),
    ('Session', {'accessToken': 'fictitious', 'tokenType': 'Bearer', 'expiresIn': 601, 'expiresAt': '2026-10-13T12:10:00Z'}),
    ('User', {'userId': '550e8400-e29b-41d4-a716-446655440000', 'email': 'person@example.com', 'displayName': '', 'active': True, 'password': 'secret'}),
]
for name, invalid in negative_cases:
    assert list(schema_validator(name).iter_errors(invalid)), f'{name} accepted invalid fixture'
for length in (12, 128):
    schema_validator('RegisterRequest').validate({'email': 'person@example.com', 'password': '界' * length})
print(f'{len(negative_cases)} negative cases and Unicode password boundary cases passed.')

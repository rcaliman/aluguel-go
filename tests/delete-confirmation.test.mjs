import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('../web/static/js/go-app.js', import.meta.url), 'utf8');
function clickDelete(path, confirmed) {
  let onClick, message, submitted = false;
  const form = { action: '', submit() { submitted = true; } };
  const button = {
    dataset: { delete: path },
    closest() { return { innerText: 'Maria da Silva\n CPF 123' }; },
    addEventListener(type, callback) { assert.equal(type, 'click'); onClick = callback; },
  };
  runInNewContext(source, {
    document: {
      addEventListener(type, callback) { assert.equal(type, 'DOMContentLoaded'); callback(); },
      querySelectorAll() { return [button]; },
      getElementById(id) { assert.equal(id, 'delete-form'); return form; },
    },
    window: { confirm(text) { message = text; return confirmed; } },
  });
  onClick();
  return { message, submitted, action: form.action };
}
test('canceling deletion never submits the form', () => {
  const result = clickDelete('/tenants/delete/1', false);
  assert.equal(result.submitted, false);
  assert.equal(result.action, '');
});
test('tenant deletion explains vacancy and identifies the record', () => {
  const result = clickDelete('/tenants/delete/1', true);
  assert.match(result.message, /Maria da Silva/);
  assert.match(result.message, /imóveis vinculados ficarão vagos/);
  assert.equal(result.submitted, true);
  assert.equal(result.action, '/tenants/delete/1');
});
test('property deletion explains loss of history', () => {
  assert.match(clickDelete('/properties/delete/3', true).message, /histórico deste imóvel será apagado/);
});

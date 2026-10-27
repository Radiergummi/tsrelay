import * as schemas from "@/api/schemas";
import callAdmin from "./fixtures/call-admin.json";
import callBin from "./fixtures/call-bin.json";
import callCut from "./fixtures/call-cut.json";
import hostileForm from "./fixtures/call-hostile-form.json";
import hostileHtml from "./fixtures/call-hostile-html.json";
import hostileJson from "./fixtures/call-hostile-json.json";
import callJson from "./fixtures/call-json.json";
import callPending from "./fixtures/call-pending.json";
import callRefused from "./fixtures/call-refused.json";
import callReplay from "./fixtures/call-replay.json";
import callUncaptured from "./fixtures/call-uncaptured.json";
import busyCalls from "./fixtures/calls-busy.json";
import calls from "./fixtures/calls.json";
import busy from "./fixtures/hook-busy.json";
import hostile from "./fixtures/hook-hostile.json";
import idle from "./fixtures/hook-idle.json";
import theirs from "./fixtures/hook-theirs.json";
import hook from "./fixtures/hook.json";
import hooksAll from "./fixtures/hooks-all.json";
import hooks from "./fixtures/hooks.json";
import meAdmin from "./fixtures/me-admin.json";
import meNobody from "./fixtures/me-nobody.json";
import meOther from "./fixtures/me-other.json";
import me from "./fixtures/me.json";

/** fixtures are real API responses from the dev harness, parsed by the app's schemas. */
export const fixtures = {
  me: schemas.me.parse(me),
  meAdmin: schemas.me.parse(meAdmin),
  meNobody: schemas.me.parse(meNobody),
  meOther: schemas.me.parse(meOther),
  hooks: schemas.hooks.parse(hooks),
  hooksAll: schemas.hooks.parse(hooksAll),
  hook: schemas.hook.parse(hook),
  idle: schemas.hook.parse(idle),
  busy: schemas.hook.parse(busy),
  hostile: schemas.hook.parse(hostile),
  theirs: schemas.hook.parse(theirs),
  calls: schemas.summaries.parse(calls),
  busyCalls: schemas.summaries.parse(busyCalls),
  callJson: schemas.call.parse(callJson),
  callReplay: schemas.call.parse(callReplay),
  callCut: schemas.call.parse(callCut),
  callBin: schemas.call.parse(callBin),
  callRefused: schemas.call.parse(callRefused),
  callPending: schemas.call.parse(callPending),
  callUncaptured: schemas.call.parse(callUncaptured),
  callAdmin: schemas.call.parse(callAdmin),
  hostileHtml: schemas.call.parse(hostileHtml),
  hostileJson: schemas.call.parse(hostileJson),
  hostileForm: schemas.call.parse(hostileForm),
};

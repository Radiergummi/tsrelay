import * as s from "@/api/schemas";
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
  me: { ...s.me.parse(me), version: "v0.3.0" },
  meAdmin: { ...s.me.parse(meAdmin), version: "v0.3.0" },
  meNobody: { ...s.me.parse(meNobody), version: "v0.3.0" },
  meOther: { ...s.me.parse(meOther), version: "v0.3.0" },
  hooks: s.hooks.parse(hooks),
  hooksAll: s.hooks.parse(hooksAll),
  hook: s.hook.parse(hook),
  idle: s.hook.parse(idle),
  busy: s.hook.parse(busy),
  hostile: s.hook.parse(hostile),
  theirs: s.hook.parse(theirs),
  calls: s.summaries.parse(calls),
  busyCalls: s.summaries.parse(busyCalls),
  callJson: s.call.parse(callJson),
  callReplay: s.call.parse(callReplay),
  callCut: s.call.parse(callCut),
  callBin: s.call.parse(callBin),
  callRefused: s.call.parse(callRefused),
  callPending: s.call.parse(callPending),
  callAdmin: s.call.parse(callAdmin),
  hostileHtml: s.call.parse(hostileHtml),
  hostileJson: s.call.parse(hostileJson),
  hostileForm: s.call.parse(hostileForm),
};

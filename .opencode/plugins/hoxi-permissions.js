// .opencode/plugins/hoxi-permissions.js
import { commandCandidates, splitCommandChain, decidePermission } from "../lib/permission-policy.js";

export const HoxiPermissionsPlugin = async () => {
  return {
    "permission.ask": async (input, output) => {
      output.status = decidePermission(input);
    },
  };
};

Object.assign(HoxiPermissionsPlugin, {
  commandCandidates,
  splitCommandChain,
  decidePermission,
});

export default HoxiPermissionsPlugin;

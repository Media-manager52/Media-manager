package postgres

// roundPolicyCurrentSQL is the one definition of "this round still runs under
// the profile's current rules". policy_version is the profile's edit token and
// is bumped by every edit, including a rename or a default change, so a version
// match alone would park rounds whose rules never changed. A round whose frozen
// policy is identical (jsonb equality) to the profile's current policy is
// therefore current as well. Any real change to the rules still makes the
// round stale.
func roundPolicyCurrentSQL(round, profile string) string {
	return "(" + round + ".policy_version=" + profile + ".policy_version OR " + round + ".policy_snapshot=" + profile + ".policy)"
}

package postgres

// roundPolicyCurrentSQL is the one definition of "this round still runs under
// the profile's current rules". policy_version is the profile's edit token and
// is bumped by every edit, including a rename or a default change, so a version
// match alone would park rounds whose rules never changed. A round whose frozen
// policy equals the profile's current policy is therefore current as well.
// The comparison is on the canonical form (policyRulesSQL): saving a profile
// writes every language list, so a list stored as [] must equal one that an
// older snapshot simply omits. Any real change to the rules still makes the
// round stale.
func roundPolicyCurrentSQL(round, profile string) string {
	return "(" + round + ".policy_version=" + profile + ".policy_version OR " + samePolicyRulesSQL(round+".policy_snapshot", profile+".policy") + ")"
}

// samePolicyRulesSQL compares two policy documents by their rules.
func samePolicyRulesSQL(left, right string) string {
	return "(" + left + "=" + right + " OR " + policyRulesSQL(left) + "=" + policyRulesSQL(right) + ")"
}

// policyRulesSQL is a policy document without its empty lists. An empty list
// and a missing key mean the same thing for every policy field (no rules, no
// required or preferred languages, no original-language routing); every
// non-empty value is kept exactly. A legacy array-form policy is returned as-is.
func policyRulesSQL(document string) string {
	return "(CASE WHEN jsonb_typeof(" + document + ")='object' THEN (SELECT COALESCE(jsonb_object_agg(item.key,item.value),'{}'::jsonb) FROM jsonb_each(" + document + ") item WHERE item.value<>'[]'::jsonb) ELSE " + document + " END)"
}

package helpdesk

// SeedArticles is demo content for a fictional clinic-operations product ("ClinicDesk"): the kind of help
// centre a support agent answers from. Two articles are admin-only, to exercise role-filtered retrieval.
var SeedArticles = []Article{
	{Title: "Resetting a staff member's password", Category: "accounts", Audience: AudienceAll,
		Body: "Open Settings > Staff, select the staff member and choose Send reset link. The link expires after 30 minutes. If the email does not arrive, check the address on the staff profile and the clinic's spam filter."},
	{Title: "Adding a new staff account", Category: "accounts", Audience: AudienceAll,
		Body: "Clinic managers can add staff under Settings > Staff > Invite. Choose a role (reception, practitioner or manager). Invites expire after 7 days and can be re-sent."},
	{Title: "Booking an appointment from the calendar", Category: "appointments", Audience: AudienceAll,
		Body: "Click an empty slot in the calendar, search for the patient by name or phone number, pick the treatment and confirm. Double bookings are blocked unless the practitioner allows overlapping slots."},
	{Title: "Rescheduling or cancelling an appointment", Category: "appointments", Audience: AudienceAll,
		Body: "Drag the appointment to a new slot to reschedule. To cancel, open it and choose Cancel; a cancellation reason is required and the patient receives an SMS if notifications are on."},
	{Title: "Appointment reminders are not being sent", Category: "notifications", Audience: AudienceAll,
		Body: "Check Settings > Notifications: SMS reminders must be enabled and the clinic must have SMS credit. Reminders go out 24 hours before the appointment. Patients who opted out of SMS are skipped."},
	{Title: "Printing or emailing a receipt", Category: "billing", Audience: AudienceAll,
		Body: "Open the paid invoice and choose Print or Email receipt. Receipts show the clinic's business registration number, which is set in Settings > Clinic profile."},
	{Title: "Refunding a payment", Category: "billing", Audience: AudienceAll,
		Body: "Open the invoice, choose Refund and enter the amount. Card refunds return to the original card in 3 to 5 business days. Partial refunds are allowed; the invoice status changes to Partially refunded."},
	{Title: "Skin analysis results are blank", Category: "skin-analysis", Audience: AudienceAll,
		Body: "Blank results usually mean the photo failed the quality check. Retake the photo in even lighting with no makeup or filters, at the distance shown on the guide overlay. If results stay blank, contact support with the analysis ID."},
	{Title: "Exporting patient lists", Category: "reports", Audience: AudienceAll,
		Body: "Reports > Patients > Export creates a CSV of the filtered list. Exports include contact details only for patients who consented to marketing."},
	{Title: "Two clinics showing in one account", Category: "accounts", Audience: AudienceAll,
		Body: "Accounts with several branches show a branch switcher in the top bar. Staff only see branches they were invited to. Reports can be filtered by branch or combined."},
	{Title: "The kiosk check-in screen is frozen", Category: "devices", Audience: AudienceAll,
		Body: "Close and reopen the kiosk app. If it still freezes, check the tablet's Wi-Fi connection and that the app is on the latest version. Kiosks need outbound access on port 443."},
	{Title: "Changing treatment prices", Category: "billing", Audience: AudienceAll,
		Body: "Managers can edit prices under Settings > Treatments. New prices apply to appointments booked after the change; existing bookings keep the old price."},
	{Title: "Escalation policy for data incidents", Category: "security", Audience: AudienceAdmin,
		Body: "Any report of patient data shown to the wrong clinic is a data incident: escalate the ticket immediately, mark it urgent, and page the on-call engineer. Do not share screenshots of patient data in the ticket."},
	{Title: "Restoring a deleted patient record", Category: "security", Audience: AudienceAdmin,
		Body: "Deleted patient records are kept for 30 days. Admins can restore them from Admin > Recycle bin. After 30 days they are purged and can only be recovered from backups with engineering approval."},
}

// SeedTickets gives a demo instance something to look at.
var SeedTickets = []NewTicket{
	{Subject: "SMS reminders stopped yesterday", Body: "Downtown branch says patients did not get reminders for today's appointments.", Priority: PriorityHigh, Requester: "downtown-reception"},
	{Subject: "Refund not showing on card", Body: "Patient refunded 4 days ago says the money is not back yet.", Priority: PriorityNormal, Requester: "riverside-manager"},
	{Subject: "Kiosk stuck on loading", Body: "Check-in kiosk at the front desk has been on the loading screen since opening.", Priority: PriorityUrgent, Requester: "downtown-reception"},
}

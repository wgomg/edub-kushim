export const SETTINGS_SECTIONS = [
	{
		id: 'intake',
		path: '/settings/intake',
		title: 'Intake',
		group: 'Pipeline',
		number: '1',
		short: 'Uploads, inbox, formats',
		description: 'Server limits, watched inbox, accepted formats, Office conversion.',
		keywords: 'upload server port host inbox office converter mime format'
	},
	{
		id: 'processing',
		path: '/settings/processing',
		title: 'Processing',
		group: 'Pipeline',
		number: '2',
		short: 'OCR, extraction, rendering',
		description: 'OCR engines, text extraction, PDF optimization, thumbnails, throughput.',
		keywords: 'ocr tesseract gosseract pdftotext qpdf ghostscript dpi workers'
	},
	{
		id: 'enrichment',
		path: '/settings/enrichment',
		title: 'Enrichment',
		group: 'Pipeline',
		number: '3',
		short: 'LLM analysis, summaries, tags',
		description: 'Content analysis, tag intelligence, scheduled maintenance, capacity.',
		keywords: 'llm analysis tags matcher adjudicator quality reconcile audit measure'
	},
	{
		id: 'storage',
		path: '/settings/storage',
		title: 'Storage',
		group: 'Pipeline',
		number: '4',
		short: 'Files, trash, database',
		description: 'Consumption and storage directories, trash retention, database.',
		keywords: 'files database postgres trash retention migration'
	},
	{
		id: 'models',
		path: '/settings/models',
		title: 'AI providers & models',
		group: 'Catalog',
		short: 'Providers, endpoints, capabilities',
		description: 'One source of truth for every provider, endpoint and model capability.',
		keywords: 'llm openai anthropic google groq deepseek local provider model catalog token'
	},
	{
		id: 'safety',
		path: '/settings/safety',
		title: 'Safety',
		group: 'Operations',
		short: 'Backups and mirror',
		description: 'Backup schedules, destinations and the mirror.',
		keywords: 'backup restore mirror rsync schedule'
	},
	{
		id: 'access',
		path: '/settings/access',
		title: 'Access',
		group: 'Operations',
		short: 'Sign-in, accounts, roles',
		description: 'Sign-in requirement, accounts and roles.',
		keywords: 'auth login users roles admin editor viewer password'
	},
	{
		id: 'system',
		path: '/settings/system',
		title: 'System',
		group: 'Operations',
		short: 'Logging and retention',
		description: 'Logging level, rotation and retention.',
		keywords: 'logs logging level debug info rotation retention version'
	}
];

export const SETTINGS_SECTION_BY_ID = Object.fromEntries(
	SETTINGS_SECTIONS.map((section) => [section.id, section])
);

export const SETTINGS_SECTION_TITLES = Object.fromEntries(
	SETTINGS_SECTIONS.map((section) => [section.id, section.title])
);

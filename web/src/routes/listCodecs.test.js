import { describe, it, expect, vi } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/paths', () => ({ resolve: (path) => path }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://localhost/') } }));

import { batchesQuery, tasksQuery } from './tasks/query.js';
import { peopleQuery } from './people/query.js';
import { tagsQuery } from './tags/query.js';
import { trashQuery } from './trash/query.js';
import { usersQuery } from '../lib/components/settings/usersQuery.js';

function roundTrip(codec, state) {
	const params = new URLSearchParams();
	for (const [key, value] of codec.serialize(state)) params.append(key, value);
	return codec.parse(params);
}

const cases = [
	[
		'people',
		peopleQuery,
		'people',
		{ q: 'jane', page: 1, pageSize: 10, sortBy: 'document_count', sortOrder: 'asc' },
		{ q: '', page: 0, pageSize: 50, sortBy: 'name', sortOrder: 'desc' }
	],
	[
		'tags',
		tagsQuery,
		'tags',
		{ q: 'finance', page: 2, pageSize: 50, sortBy: 'name', sortOrder: 'asc' },
		{ q: '', page: 0, pageSize: 25, sortBy: 'document_count', sortOrder: 'desc' }
	],
	[
		'tasks',
		tasksQuery,
		'tasks',
		{ batch: 'b-7', status: 'failed', page: 2, pageSize: 50, sortBy: 'status', sortOrder: 'asc' },
		{ batch: '', status: '', page: 0, pageSize: 25, sortBy: 'task_type', sortOrder: 'desc' }
	],
	[
		'batches',
		batchesQuery,
		'batches',
		{ taskStatus: 'processing', page: 1, pageSize: 10, sortBy: 'total', sortOrder: 'asc' },
		{ taskStatus: '', page: 0, pageSize: 25, sortBy: 'batch_id', sortOrder: 'desc' }
	],
	[
		'trash',
		trashQuery,
		'trash',
		{ page: 3, pageSize: 50, sortBy: 'file_size', sortOrder: 'asc' },
		{ page: 0, pageSize: 25, sortBy: 'deleted_at', sortOrder: 'desc' }
	],
	[
		'users',
		usersQuery,
		'users',
		{ page: 1, pageSize: 10, sortBy: 'role', sortOrder: 'asc' },
		{ page: 0, pageSize: 25, sortBy: 'username', sortOrder: 'desc' }
	]
];

describe('list codecs', () => {
	it.each(cases)(
		'%s round-trips its state through serialize and parse',
		(_name, codec, _prefix, state) => {
			expect(roundTrip(codec, state)).toEqual(state);
		}
	);

	it.each(cases)(
		'%s falls back per key for invalid or absent view values',
		(_name, codec, prefix, _state, defaults) => {
			const params = new URLSearchParams(
				`${prefix}_page=-1&${prefix}_size=9999&${prefix}_order=weird`
			);
			expect(codec.parse(params)).toEqual(defaults);
		}
	);
});

let _token = '';
let _user = null;
let _authEnabled = true;

export function init() {
	_token = localStorage.getItem('token') || '';
	try {
		_user = JSON.parse(localStorage.getItem('user'));
	} catch {
		_user = null;
	}
}
init();

export function getToken() {
	return _token;
}
export function getUser() {
	return _user;
}
export function isAuthenticated() {
	return !!_token && !!_user;
}
export function login(token, user) {
	_token = token;
	_user = user;
	localStorage.setItem('token', token);
	localStorage.setItem('user', JSON.stringify(user));
}
export function logout() {
	_token = '';
	_user = null;
	localStorage.removeItem('token');
	localStorage.removeItem('user');
}
export function getRole() {
	return _user?.role ?? '';
}
export function isAdmin() {
	return getRole() === 'admin';
}
export function isEditor() {
	return getRole() === 'editor' || getRole() === 'admin';
}
export function authEnabled() {
	return _authEnabled;
}
export function setAuthEnabled(v) {
	_authEnabled = v;
}
export function roleBadgeClass(role) {
	const colors = {
		admin: 'bg-lapis-500/20 text-lapis-400',
		editor: 'bg-gold-500/20 text-gold-400',
		viewer: 'bg-clay-700/50 text-parchment-400'
	};
	return colors[role] || colors.viewer;
}

export async function refreshMe() {
	try {
		const res = await fetch('/api/v1/me', {
			headers: { Authorization: `Bearer ${_token}` }
		});
		if (res.status === 401) {
			logout();
			return;
		}
		if (res.ok) {
			const data = await res.json();
			_user = data;
			localStorage.setItem('user', JSON.stringify(data));
		}
	} catch {
		// keep stale data on network error
	}
}

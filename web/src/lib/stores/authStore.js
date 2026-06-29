let _token = '';
let _user = null;

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
